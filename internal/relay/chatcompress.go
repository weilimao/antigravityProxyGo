package relay

import (
	"encoding/json"
	"strings"
)

// chatcompress.go —— 公共上下文压缩算法（OpenAI Chat 兼容格式）
//
// 忠实迁移《Claude Code 上下文压缩算法深度分析》文档的核心三层结构与降级哲学：
//   L1 微压缩：清理较早的 role:"tool" 工具结果内容（保留近 N 个），不动 tool_calls 配对
//   L2 PTL 裁剪：按 API 轮次分组，删最旧 20% 组，配对保护回退，首条 assistant 补 user marker
//   Token 估算：roughTokenCountEstimation + 消息级 ×4/3 保守填充
//
// 设计约束：纯函数式、无外部 IO、无单例状态，可被任意 OpenAI Chat 兼容渠道复用。
// 操作对象统一为 OpenAIChatRequest（即发给 NVIDIA 上游的请求体），与 Gemini 链路 optimizer.go 解耦。

// ChatCompressor 持有压缩参数。无状态，字段仅作配置载体。
type ChatCompressor struct {
	ThresholdTokens      int // 触发压缩的估算 token 阈值（超过才启动），默认 80000
	KeepToolResults      int // L1 微压缩保留最近多少个 tool 结果原文，默认 4
	MaxCompressRetries   int // 单请求压缩重试断路器上限，默认 3
}

// ChatCompressorDefaults 是开箱即用的默认参数，供 settings.go 默认值与调用方兜底复用。
const (
	ChatCompressDefaultThreshold = 80000
	ChatCompressDefaultKeepN     = 4
	ChatCompressDefaultMaxRetry  = 3

	// ChatCompressClearText 是 L1 微压缩替换旧 tool 结果内容的占位文本。
	ChatCompressClearText = "[Old tool result content cleared]"

	// ChatCompressTruncMarker 是 L2 PTL 裁剪后首条为 assistant 时补的 user marker（对应文档 PTL_RETRY_MARKER）。
	ChatCompressTruncMarker = "(earlier context truncated)"
)

// ChatCompressTargetScale 是压缩后体积的安全系数：估算 token 落到 threshold×0.9 以内即视为"压够了"。
const ChatCompressTargetScale = 0.9

// ptlDropRatio 是 L2 裁剪时每次删除的最旧组占比（文档 §4 末：truncate 20%）。
const pttlDropRatioFloor = 1 // 至少删 1 组

// NewChatCompressor 用给定参数构造一个压缩器；零值字段回退默认。
func NewChatCompressor(threshold, keepN, maxRetry int) *ChatCompressor {
	if threshold <= 0 {
		threshold = ChatCompressDefaultThreshold
	}
	if keepN <= 0 {
		keepN = ChatCompressDefaultKeepN
	}
	if maxRetry <= 0 {
		maxRetry = ChatCompressDefaultMaxRetry
	}
	return &ChatCompressor{
		ThresholdTokens:    threshold,
		KeepToolResults:    keepN,
		MaxCompressRetries: maxRetry,
	}
}

// EstimateChatTokens 按文档 §7 roughTokenCountEstimation 估算 messages 序列的 token 数。
// 思路：将所有可见文本（content + tool_calls 名/参 + role）按 len/4 估算粗 token，
// 最后整体 ×4/3 做 33% 保守填充（与文档保守估算一致）。
func EstimateChatTokens(msgs []ChatMessage) int {
	var sum int
	for _, m := range msgs {
		sum += roughTokens(m.Text())
		for _, tc := range m.ToolCalls {
			sum += roughTokens(tc.Function.Name)
			sum += roughTokens(tc.Function.Arguments)
			sum += roughTokens(tc.ID)
		}
		sum += roughTokens(m.ToolCallID)
		sum += roughTokens(m.ToolName)
	}
	// 与消息级 ×4/3 保守填充等价于总和 ×4/3，避免浮点
	return (sum * 4) / 3
}

// roughTokens 是文档 §7 的 len(s)/4 粗估。注意：([]rune s) 会更准但更高耗，这里保持与文档一致用字节长度。
func roughTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s) / 4
}

// Compress 是压缩入口：两级阶梯降级 + 终止条件。
// 返回值：trimmed 的新请求体副本（不修改入参），ok=true 表示确实发生了有效压缩（体积变小）；ok=false 表示无需压缩或压缩无效。
// 调用方据 ok=true 才覆写出站请求体并重发上游。
func (c *ChatCompressor) Compress(req *OpenAIChatRequest) (newReq *OpenAIChatRequest, ok bool) {
	if req == nil || len(req.Messages) == 0 {
		return req, false
	}
	est0 := EstimateChatTokens(req.Messages)
	if est0 <= c.ThresholdTokens {
		// 未超阈值，无需压缩
		return req, false
	}

	// 拷贝一份，绝不修改入参
	out := cloneChatRequest(req)
	targetFloor := int(float64(c.ThresholdTokens) * ChatCompressTargetScale)

	// L1 微压缩
	if microMsgs, did := microCompress(out.Messages, c.KeepToolResults); did {
		out.Messages = microMsgs
		if EstimateChatTokens(out.Messages) <= targetFloor {
			return shrinkOnlyIfSmaller(req, out)
		}
	}
	// 仍超阈值 → L2 PTL 裁剪
	if truncMsgs, did := pttlTruncate(out.Messages); did {
		out.Messages = truncMsgs
	}
	return shrinkOnlyIfSmaller(req, out)
}

// shrinkOnlyIfSmaller 比较"原请求 marshal 后字节数"与"压缩后请求 marshal 后字节数"，
// 仅当确实变小才视为有效压缩（ok=true）。与文档"压缩体小于原体才算真压缩"对齐。
func shrinkOnlyIfSmaller(orig, compressed *OpenAIChatRequest) (*OpenAIChatRequest, bool) {
	ob, e1 := jsonMarshalChatRequest(orig)
	cb, e2 := jsonMarshalChatRequest(compressed)
	if e1 != nil || e2 != nil {
		// 兜底：用消息数比较
		return compressed, len(compressed.Messages) < len(orig.Messages)
	}
	if len(cb) < len(ob) {
		return compressed, true
	}
	return orig, false
}

// cloneChatRequest 深拷贝请求体，确保压缩过程不污染入参/不与其他重试共享切片。
func cloneChatRequest(req *OpenAIChatRequest) *OpenAIChatRequest {
	if req == nil {
		return nil
	}
	cp := *req
	if req.Messages != nil {
		cp.Messages = make([]ChatMessage, len(req.Messages))
		for i, m := range req.Messages {
			cp.Messages[i] = cloneChatMessage(m)
		}
	}
	if req.Tools != nil {
		// tools 不参与压缩，浅拷贝足矣（不修改）
		cp.Tools = req.Tools
	}
	return &cp
}

// cloneChatMessage 深拷贝单条消息，重点拷贝 ToolCalls 切片以避免共享底层数组。
func cloneChatMessage(m ChatMessage) ChatMessage {
	cp := m
	if m.ToolCalls != nil {
		cp.ToolCalls = make([]ChatToolCall, len(m.ToolCalls))
		copy(cp.ToolCalls, m.ToolCalls)
	}
	return cp
}

// jsonMarshalChatRequest 安全序列化请求体，失败返回 nil+err。
func jsonMarshalChatRequest(req *OpenAIChatRequest) ([]byte, error) {
	return json.Marshal(req)
}

// ----------------------------------------------------------------------------
// L1 微压缩（文档 §2.1 子路径A）
// ----------------------------------------------------------------------------

// microCompress 清理较早的 role:"tool" 消息的 Content：
//   - 按 messages 顺序收集所有 tool 结果消息的索引（保持时间序）；
//   - 保留最近 keepN 个 tool 结果的原文,更早的把 Content 替换为 ChatCompressClearText;
//   - 保留 ToolCallID/ToolName,不动 assistant 的 tool_calls,保证配对完整;
//   - did=true 表示确实替换了至少一条。压缩是"有损"的,语义可接受。
func microCompress(msgs []ChatMessage, keepN int) ([]ChatMessage, bool) {
	if len(msgs) == 0 {
		return msgs, false
	}
	if keepN < 0 {
		keepN = 0
	}
	// 收集 tool 消息索引
	var toolIdx []int
	for i, m := range msgs {
		if m.Role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	total := len(toolIdx)
	if total <= keepN {
		// 没有可清理的旧 tool 结果,L1 无效
		return msgs, false
	}
	dropCount := total - keepN
	out := make([]ChatMessage, len(msgs))
	copy(out, msgs)
	for i := 0; i < dropCount; i++ {
		idx := toolIdx[i]
		// 仅当内容确实还有值时才替换,避免重复替换造成"伪降级"
		if strings.TrimSpace(out[idx].Text()) != "" && out[idx].Content != ChatCompressClearText {
			out[idx].Content = ChatCompressClearText
		}
	}
	return out, true
}

// ----------------------------------------------------------------------------
// L2 PTL 裁剪（文档 §4 末 truncateHeadForPTLRetry + §6 分组 + §5.4 配对保护）
// ----------------------------------------------------------------------------

// groupMessagesByApiRound 将 messages 按 API 轮次分组（文档 §6）。
// OpenAI Chat 无 message.id,改用"assistant 出现"作轮次边界:
//   - system 前导消息单独成组并标记永远保留(返回值中标记为不可删);
//   - 一个 assistant 消息及其紧随其后(直到下一个 user 之前)的所有 tool 结果划为一组;
//   - user 消息单独成组(或与紧随其后的 tool 结果合并到下一组,简化为各 user 单独成组);
// 返回分组切片,每组的 (start,end) 半开区间在原 msgs 中的下标。
type messageGroup struct {
	start, end int // [start,end) 半开区间,下标指向原 msgs
	keepLock   bool // system 锁定不可删
}

func groupMessagesByApiRound(msgs []ChatMessage) []messageGroup {
	if len(msgs) == 0 {
		return nil
	}
	var groups []messageGroup
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role == "system" {
			// system 单独成组并锁定
			j := i + 1
			// 连续的多个 system 归入同一锁定组（罕见但容错）
			for j < len(msgs) && msgs[j].Role == "system" {
				j++
			}
			groups = append(groups, messageGroup{start: i, end: j, keepLock: true})
			i = j
			continue
		}
		if m.Role == "assistant" {
			// assistant + 其后连续的 tool 结果划为一组
			j := i + 1
			for j < len(msgs) && msgs[j].Role == "tool" {
				j++
			}
			groups = append(groups, messageGroup{start: i, end: j})
			i = j
			continue
		}
		// user 或其他角色:单独成组(其后若紧跟 tool 不并入,极小概率边界场景,简化处理)
		groups = append(groups, messageGroup{start: i, end: i + 1})
		i++
	}
	return groups
}

// pttlTruncate 删最旧 20% 组(至少 1 组),带配对保护回退。
// 返回裁剪后的 messages 与是否真裁剪。
//   - system 锁定组永不删除(始终前缀保留);
//   - 删除边界若切断 assistant→tool 配对(tool_call_id 配对),回退到不切断处;
//   - 裁剪后若首条是 assistant 且无前置 system,补一条 user marker(PTL_RETRY_MARKER)。
func pttlTruncate(msgs []ChatMessage) ([]ChatMessage, bool) {
	groups := groupMessagesByApiRound(msgs)
	if len(groups) == 0 {
		return msgs, false
	}

	// 把"锁定组(system,永远在前)"与"可删组"分离。
	// 前导若干连续的锁定组构成 fixedTail 区间 [0, fixedEnd),始终保留;
	// 可删组序列 keepers 从 fixedEnd 之后开始。
	fixedEnd := 0
	for fixedEnd < len(groups) && groups[fixedEnd].keepLock {
		// 只认同"位于消息数组最前面的连续锁定组"为不可删前导;
		// 中途出现的 system(罕见)不当作前导锁定,避免逻辑歧义。
		if groups[fixedEnd].start > fixedEnd {
			break // start 不对齐,非纯前导
		}
		fixedEnd++
	}
	var keepers []messageGroup
	for _, g := range groups {
		if !g.keepLock {
			keepers = append(keepers, g)
		}
	}
	if len(keepers) == 0 {
		return msgs, false
	}

	// 在可删组序列上删最旧 20%(至少 1 组,至多 len-1 以保证至少留 1 组)。
	dropCount := len(keepers) / 5
	if dropCount < pttlDropRatioFloor {
		dropCount = pttlDropRatioFloor
	}
	if dropCount >= len(keepers) {
		dropCount = len(keepers) - 1
	}
	if dropCount <= 0 {
		return msgs, false
	}

	// 被删的最后一条消息的下标 + 1 即为"保留区起点"在原 msgs 中的下标。
	// 注意:被删的 keeper 组可能并非在原 msgs 中连续(中间夹了锁定组),
	// 但前导锁定组已在 fixedEnd 内统一保留,keepers 在 fixedEnd 之后是连续的,
	// 所以"删前 dropCount 个 keeper"对应的保留起点 = keepers[dropCount-1].end。
	dropLast := keepers[dropCount-1]
	cutIdx := dropLast.end
	if cutIdx <= 0 || cutIdx >= len(msgs) {
		return msgs, false
	}

	// 配对保护:回退裁剪线到不切断 tool_calls→tool(tool_call_id) 配对的位置
	cutIdx = adjustIndexToPreservePairings(msgs, cutIdx)
	if cutIdx <= 0 || cutIdx >= len(msgs) {
		return msgs, false
	}

	// 前导锁定组(system)始终保留在前;裁剪只作用于其后。
	fixedBytes := 0
	if fixedEnd > 0 {
		fixedBytes = groups[fixedEnd-1].end
	}
	if cutIdx < fixedBytes {
		// 配对回退过头,越过 system 前导 → 强制不低于前导终点
		cutIdx = fixedBytes
	}
	out := make([]ChatMessage, 0, fixedBytes+(len(msgs)-cutIdx))
	if fixedBytes > 0 {
		out = append(out, msgs[:fixedBytes]...)
	}
	out = append(out, msgs[cutIdx:]...)

	// 裁剪后首条 assistant(且前面无 system 前导)且无前置 system → 补 user marker
	if len(out) > 0 && out[0].Role == "assistant" && fixedBytes == 0 {
		marker := ChatMessage{Role: "user", Content: ChatCompressTruncMarker}
		out = append([]ChatMessage{marker}, out...)
	}
	return out, true
}

// adjustIndexToPreservePairings(文档 §5.4) 把裁剪线 cutIdx 回退到不切断配对的位置:
//   - 若 cutIdx 落在 assistant 之后紧跟 tool 区间中间(即 cutIdx 指向某条 tool),
//     则把裁剪线回退到该 assistant 之前,以保持 assistant.tool_calls 与 tool.tool_call_id 整组保留;
//   - 若 cutIdx 指向的恰好是 assistant 的起始,则向前回退到上一条非 tool 结尾;
//   - 若 cutIdx 处恰好是 user,本就安全,不调整。
func adjustIndexToPreservePairings(msgs []ChatMessage, cutIdx int) int {
	if cutIdx <= 0 || cutIdx >= len(msgs) {
		return cutIdx
	}
	// 回退:若 cutIdx 处恰好是 tool(说明切进了 assistant+tool 组的中间),向前找到该 assistant 之前
	for cutIdx > 0 && msgs[cutIdx].Role == "tool" {
		// 找到这个 tool 组前面的 assistant 起点
		k := cutIdx
		for k > 0 && msgs[k-1].Role == "tool" {
			k--
		}
		// msgs[k-1] 应是 assistant；把裁剪线整体回退到 assistant 之前(连同它一起保留)
		if k > 0 && msgs[k-1].Role == "assistant" {
			k--
		}
		// 不再回退到 user 之后(那会留下孤立的 user 重复);直接定到 k
		cutIdx = k
		break
	}
	// 若 cutIdx 处是 assistant(裁剪线正好落在 assistant 起点)，向前看一条是否是 tool 结尾；
	// 极小概率 cutIdx 落在 user，安全，不处理。
	if cutIdx < len(msgs) && msgs[cutIdx].Role == "assistant" && cutIdx > 0 {
		// assistant 起点，安全(不切断它和其后 tool)
		return cutIdx
	}
	// 兜底:确保 cutIdx 不越界
	if cutIdx < 0 {
		cutIdx = 0
	}
	if cutIdx >= len(msgs) {
		cutIdx = len(msgs) - 1
	}
	return cutIdx
}
