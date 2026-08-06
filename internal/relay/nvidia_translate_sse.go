package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// nvidia_translate_sse.go: OpenAI Chat SSE -> Anthropic SSE 流式转译主循环 + sseBlockStates 状态机 + watchCancel。
// 从 nvidia_translate.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。

// OpenAIChatSSEToAnthropicSSE 实时把 NVIDIA(OpenAI Chat 兼容)的 SSE 流重写成 Anthropic Messages SSE 事件流。
// reader 读上游 SSE，writer 写 Anthropic SSE。返回累计 input/output/cached tokens。
// cached 取上游末帧 usage 的缓存命中口径(prompt_cache_hit_tokens 或 prompt_tokens_details.cached_tokens,
// 由 OpenAIChatUsage.CachedTokens() 统一解析),供 Other 号池流式回译链路(replyOpenAIToAnthropic)
// 透传给 recordOtherUsage 驱动缓存命中率/CacheStatus。上游无 cache 字段时返回 0,不报错。
// 协议事件序列：message_start → (content_block_start/delta/stop × N) → message_delta(usage) → message_stop。
//
// ctx 为入站请求的 r.Context()：客户端主动取消时 ctx 被撤销，watchCancel 立即 Close 上游 body,
// 使阻塞的 scanner.Scan() 退出读循环，随后在循环外统一补发 message_delta + message_stop 尾帧。
// body 为上游响应体(用于 ctx 取消时主动 Close 触发 scanner 退出);body 为 nil 时仅退化兼容旧行为,
// 不接入"取消即断"(留给调用方保证非空)。
//
// 本函数为薄委托:将 sink 具体化为 flushWriter(写往 *bufio.Writer + 可选 http.Flusher),
// 真正的转译逻辑在 openAIChatSSEToAnthropicSSEInto(接收 sseEventSink)。蓄流回放重试链路
// 直接调 openAIChatSSEToAnthropicSSEInto 传 replayWriter,本函数签名保持不变,所有旧调用零改动。
//
// inputTokens 为入站请求本地估算的输入 token 数(保底 1),写入 message_start.usage.input_tokens,
// 让客户端(Claude Code spinner 进行中)在流首即可显示 ↑(否则流首为 0 会让 spinner 只有 ↓ 无 ↑)。
// 真实累计值仍由末帧 message_delta.usage 覆盖,结算精度不受影响。
func OpenAIChatSSEToAnthropicSSE(ctx context.Context, reader io.Reader, body io.ReadCloser, writer *bufio.Writer, model string, inputTokens int, flusher ...http.Flusher) (input, output, cached int, err error) {
	streamID := fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	fw := newFlushWriter(streamID, writer, flusher...)
	input, output, cached, _, _, err = openAIChatSSEToAnthropicSSEInto(ctx, reader, body, fw, streamID, model, inputTokens)
	return input, output, cached, err
}

// openAIChatSSEToAnthropicSSEInto 把上游 OpenAI Chat SSE 翻译成 Anthropic SSE,写到 sink(flushWriter 或 replayWriter)。
// 返回 (input, output, finishEmitted, streamTerminated, err):
//   - finishEmitted:是否收到上游合法 finish_reason 帧并已 close 所有 block(确定性"本轮正文收尾"信号);
//   - streamTerminated:上游流是否以正常协议终止符结束 —— 收到 [DONE] 或读到正常 EOF(无扫描错误)。
//     这一路径的完整性兜底:NIM 等上游存在"不发 finish_reason 帧,仅发 usage 帧后跟 [DONE]"的合法收尾形态,
//     仅靠 finishEmitted 会把它误判为不完整断流。streamTerminated==true 表示上游流是"协议级结束"而非断流,
//     重试主体据此判定可回放。
//   - err:上游 SSE 内嵌 error chunk 或 scanner.Err()(非 ctx 取消)的错误,供上层日志/重试判定。
//
// 完整性判定(重试主体使用):finishEmitted || (streamTerminated && err==nil) 视为完整可回放。
// 真·断流(unexpected EOF 在 [DONE] 之前打断)会使 streamTerminated=false 且 err!=nil,两条件均不满足 → 重试。
//
// 转译逻辑与原 OpenAIChatSSEToAnthropicSSE 逐行等价,仅参数化 sink 与 streamID,并新增 streamTerminated 信号。
//
// inputTokens 写入 message_start.usage.input_tokens(保底 1),让客户端流首即有非零 ↑;
// 真实累计值由末帧 message_delta.usage 覆盖。参见 OpenAIChatSSEToAnthropicSSE 的 inputTokens 注释。
func openAIChatSSEToAnthropicSSEInto(ctx context.Context, reader io.Reader, body io.ReadCloser, sink sseEventSink, streamID, model string, inputTokens int) (input, output, cached int, finishEmitted, streamTerminated bool, err error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// 客户端取消即断：ctx.Done() → Close 上游 body → scanner.Scan() 立即返回
	if ctx != nil && body != nil {
		stop := watchCancel(ctx, body)
		defer stop()
	}

	// message_start
	sink.writeEvent("message_start", messageStartPayload(streamID, model, inputTokens))

	blockStates := &sseBlockStates{blocks: map[int]*sseBlock{}}
	stopReason := ""

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			streamTerminated = true // OpenAI 协议权威终止符:流正常结束,非断流。
			break
		}

		type sseErrorChunk struct {
			Error *struct {
				Message string      `json:"message"`
				Type    string      `json:"type"`
				Code    interface{} `json:"code"`
			} `json:"error"`
		}
		var errChunk sseErrorChunk
		if json.Unmarshal([]byte(data), &errChunk) == nil && errChunk.Error != nil && errChunk.Error.Message != "" {
			errMsg := fmt.Sprintf("upstream sse error: %s (code: %v)", errChunk.Error.Message, errChunk.Error.Code)
			// 保住 err 供上层(writeNvidiaAnthropicStream 忽略返回值,但 watchCancel/日志可取)
			// 仅在尚未被 ctx 取消语义占据时记录上游 error,避免覆盖既有 ctx 取消路径的语义。
			if err == nil {
				err = fmt.Errorf("%s", errMsg)
			}
			if !blockStates.hasEmittedAnyBlock() {
				// 历史缺口:此处曾直接 return,跳过循环外统一尾帧补发,
				// 导致 CLI 仅收到 message_start 而无 message_stop → 卡等尾帧、
				// 表现为"断了不干活"。现改为保底发一个文本块并 break,让控制流落到循环外
				// 统一补 message_delta + message_stop,产出完整闭合的 SSE 流(空本轮)。
				// 取舍:CLI 视为本轮正常结束(end_turn),不卡等、不触发重试风暴;
				// 上游 error 原文已保存在 err 并由代理日志记录,便于事后排查。
				blockStates.ensureAtLeastOneBlock(sink)
			}
			break
		}

		var chunk OpenAIChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 跳过无法解析的行，但不中断流
			continue
		}
		if chunk.Usage != nil {
			input = chunk.Usage.PromptTokens
			output = chunk.Usage.CompletionTokens
			cached = chunk.Usage.CachedTokens()
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Content != "" {
			blockStates.emitTextDelta(ch.Delta.Content, sink)
		} else if ch.Delta.ReasoningContent != "" {
			// 仅在非空时进 thinking 分支:无推理模型(reasoning_content 恒空)永不开 thinking 块,
			// 若开启 IsReasoningAsText() 伪装模式,则作为普通 text_delta 直接逐字推打屏幕,避免 CLI 界面自动折叠。
			if IsReasoningAsText() {
				blockStates.emitTextDelta(ch.Delta.ReasoningContent, sink)
			} else {
				blockStates.emitThinkingDelta(ch.Delta.ReasoningContent, sink)
			}
		} else if ch.Delta.Reasoning != "" {
			// 兜底:部分 NIM 上游模型思考文本走 reasoning 字段(而非 reasoning_content),
			// 同样在伪装模式下转为普通 text_delta。
			if IsReasoningAsText() {
				blockStates.emitTextDelta(ch.Delta.Reasoning, sink)
			} else {
				blockStates.emitThinkingDelta(ch.Delta.Reasoning, sink)
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			blockStates.emitToolCallDelta(tc, sink)
		}
		if ch.FinishReason != nil && ch.FinishReason != "" {
			blockStates.ensureAtLeastOneBlock(sink)
			blockStates.closeAll(sink)
			stopReason = blockStates.determineStopReason(fmt.Sprintf("%v", ch.FinishReason))
			finishEmitted = true
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && scanErr != io.EOF {
		// ctx 取消触发的 body.Close() 会让 Scan() 返回 "read on closed *" 类错误,
		// 这属于"客户端主动取消"的正常收尾路径,不应作为 err 上抛(避免调用方误判为上游故障),
		// 走尾帧补发即可。
		if ctx == nil || ctx.Err() == nil {
			err = scanErr
		}
	} else if err == nil {
		// 循环无扫描错误且 err 仍 nil(未被上游 SSE error chunk 等主动 break 路径污染):
		// 上游流以正常 EOF 关闭,协议级结束,非断流。[DONE] 分支已提前置位;此处覆盖"上游未发 [DONE]
		// 即 EOF 关闭"的合法收尾形态。err!=nil(如 error chunk break)则保持 streamTerminated=false,
		// 让重试主体据此判定为不完整 → 重试,符合上游报错应重试的语义。
		streamTerminated = true
	}
	// message_delta 必须在循环结束后发出：Anthropic 官方要求 message_delta.usage 的 token
	// 计数为累计值 (cumulative)，而上游 NIM 的 usage 帧 ({"choices":[],"usage":{...}}) 在
	// finish_reason 帧之后、[DONE] 之前才送达。若在 finish_reason 帧时立即发 message_delta，
	// input/output 仍为 0，会导致 Claude Code SDK 的 MessageAccumulator 误判流未正常结束，
	// 触发"等连接关闭/下次请求才整条渲染"的退化路径。此处统一在循环外、usage 帧已落地后发出。
	if !finishEmitted {
		blockStates.ensureAtLeastOneBlock(sink)
		blockStates.closeAll(sink)
		stopReason = blockStates.determineStopReason("")
	}
	// 客户端主动取消(ctx 被撤销)且上游未给出 finish_reason:补 end_turn 语义尾帧,
	// 让 Claude Code SDK 的 MessageAccumulator 视为"本轮正常结束",不触发失败重试、不卡等尾帧。
	if ctx != nil && ctx.Err() != nil && stopReason == "" {
		stopReason = "end_turn"
	}
	sink.writeEvent("message_delta", messageDeltaPayload(stopReason, input, output))
	sink.writeEvent("message_stop", `{"type":"message_stop"}`)
	sink.flush()
	return input, output, cached, finishEmitted, streamTerminated, err
}

// sseBlock 记录当前打开的内容块(文本或工具调用)在 Anthropic 流中的索引与身份。
// kind 取值:"text" | "tool_use" | "thinking"。thinking 块固定占 index 0,先于 text/tool 块,
// 一旦开过即永久占位(不从 map 删除),保证后续 text/tool 块按官方"index 单调递增不复用"分配。
type sseBlock struct {
	index           int
	kind            string // "text" | "tool_use" | "thinking"
	toolID          string
	toolName        string
	textStarted     bool
	toolStarted     bool
	thinkingStarted bool // thinking 块已开块且至少发过一条 thinking_delta 的标志
	closed          bool // 该块是否已发过 content_block_stop,避免 closeAll 重复关块
}

type sseBlockStates struct {
	mu          sync.Mutex
	blocks      map[int]*sseBlock
	next        int
	textEmitted bool
	hasToolCall bool
}

// nextFreeIndex 返回当前 blocks 中未占用的最小 index,供 text/tool 块分配使用。
// 引入 thinking 块(固定占 index 0)后,text 与 tool 块需据此整体后移一位,避免与 thinking 块抢同 index。
func (s *sseBlockStates) nextFreeIndex() int {
	used := map[int]bool{}
	for k := range s.blocks {
		used[k] = true
	}
	for i := 0; ; i++ {
		if !used[i] {
			return i
		}
	}
}

// emitTextDelta 把一条 OpenAI 文本增量转成 Anthropic content_block_delta(text_delta)。
// 若 thinking 块当前已开(thinkingStarted),先按官方序列完整闭合它
// (signature_delta → content_block_stop) 再开 text 块,保证"思考先于正文、思考块完全闭合后才开 text"。
// text 块 index 分配:若 index 0 尚未被任何块占用(无 thinking/无 tool)→ 0;
// 否则用 nextFreeIndex()(thinking 开过永久占 0 → text 落在 1)。
// 已开过的同 index text 块(连续 text_delta)直接复用、不重复开块。
func (s *sseBlockStates) emitTextDelta(text string, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 若 thinking 块当前已开:先发空串 signature_delta + content_block_stop 闭合它
	s.closeThinkingIfOpen(fw)
	// 优先检索是否有已创建且未关闭的 text 块，有则复用（避免多帧 text_delta 误触发 nextFreeIndex 开新块）
	var b *sseBlock
	for _, blk := range s.blocks {
		if blk != nil && blk.kind == "text" && !blk.closed {
			b = blk
			break
		}
	}
	if b == nil {
		// 未找到已有 text 块：分配 index（若 0 位被 thinking 或 tool 占领则用 nextFreeIndex）
		idx := 0
		if b0, ok := s.blocks[0]; ok && b0 != nil && (b0.kind == "thinking" || b0.kind == "tool_use") {
			idx = s.nextFreeIndex()
		}
		b = &sseBlock{index: idx, kind: "text"}
		s.blocks[idx] = b
	}
	if !b.textStarted {
		b.textStarted = true
		s.textEmitted = true
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "text", "", ""))
	}
	fw.writeEvent("content_block_delta", contentBlockTextDeltaPayload(b.index, text))
}

// closeThinkingIfOpen 在锁内调用:若 blocks[0] 是已开块(thinkingStarted)且尚未关闭的 thinking 块,
// 按 official 序列发 signature_delta(空)+content_block_stop 闭合它,并标记 closed,
// 但不从 map 删除——以保证后续 text/tool 块按官方"index 单调递增不复用 thinking 的 0 位"分配。
// 仅可开块却从未下发 thinking_delta 的异常 thinking 块(thinkingStarted==false)静默丢弃且不占位。
func (s *sseBlockStates) closeThinkingIfOpen(fw sseEventSink) {
	b, ok := s.blocks[0]
	if !ok || b == nil || b.kind != "thinking" || b.closed {
		return
	}
	if b.thinkingStarted {
		fw.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(b.index, ""))
		fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
		b.closed = true
	}
	// 未发过 thinking_delta 的空块:丢弃,不占位(无推理模型守卫),从 map 删除
	if !b.thinkingStarted {
		delete(s.blocks, 0)
	}
}

// emitThinkingDelta 把上游 reasoning_content 增量转成 Anthropic thinking 块事件序列:
// 首次开 content_block_start(thinking),后续 content_block_delta(thinking_delta)。
// thinking 块固定占 index 0,严格对齐官方"思考先于正文"顺序。
// 仅在 reasoning_content != "" 时由主循环调用,无推理模型路径永不开 thinking 块。
func (s *sseBlockStates) emitThinkingDelta(text string, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 若 text 块已开却回收到 reasoning(异常顺序),先关 text 块再开 thinking(防御,理论上不触发)
	if b, ok := s.blocks[0]; ok && b != nil && b.kind == "text" && b.textStarted {
		fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
		delete(s.blocks, 0)
	}
	b, ok := s.blocks[0]
	if !ok || b == nil {
		b = &sseBlock{index: 0, kind: "thinking"}
		s.blocks[0] = b
	} else if b.kind != "thinking" {
		// 防御:index 0 被非 thinking 占据时,用下一可用 index 开 thinking
		idx := s.nextFreeIndex()
		b = &sseBlock{index: idx, kind: "thinking"}
		s.blocks[idx] = b
	}
	// closed 守卫:thinking 块已被 closeThinkingIfOpen/closeAll 提前闭合(已发 content_block_stop,
	// b.closed==true,仍占 index 0 位),此时再追推 thinking_delta 会落在已 stop 的(index, thinking)块上,
	// 违反 Anthropic content_block 的(index, type)配对一致性,触发客户端 SDK 报
	// "Mismatched content block type content_block_delta thinking"。
	// 上游异常"思考→正文→再思考"交错时(部分 NIM/GLM 模型 reasoning_content 与 content 交叉下发),
	// Anthropic 协议要求思考严格先于正文、index 单调不复用,后置思考在协议上无法表达,
	// 故丢弃该异常思考分片:不改任何块状态、不发任何事件(等同已 closed 块不再收 delta 的语义)。
	// 此路径只在非 IsReasoningAsText() 模式触发;伪装模式由主循环改走 emitTextDelta,不进本函数。
	if b.closed {
		return
	}
	if !b.thinkingStarted {
		b.thinkingStarted = true
		fw.writeEvent("content_block_start", contentBlockThinkingStartPayload(b.index))
	}
	fw.writeEvent("content_block_delta", contentBlockThinkingDeltaPayload(b.index, text))
}

// emitToolCallDelta 处理 OpenAI tool_calls 增量(index 指向上游分块的工具调用编号)，
// 映射成 Anthropic 的 content_block_start(tool_use) + content_block_delta(input_json_delta)。
func (s *sseBlockStates) emitToolCallDelta(tc ChatToolCall, fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasToolCall = true
	// 若 thinking 块当前已开:先按官方序列完整闭合它(signature_delta → stop)再开 tool_use,
	// 保证"思考先于正文/工具"且 thinking 块在 tool_use 块之前完全闭合。
	s.closeThinkingIfOpen(fw)
	// tool_use 块 index 分配:base = 已开(含已关)块数量 —— thinking 开过占 1 位 + text 开过占 1 位。
	// 上游 tc.Index 是该工具调用在上游工具列表里的位次,key = base + tc.Index 保证多工具不抢 index,
	// 且工具块严格排在 thinking/text 之后,符合官方"思考→正文→工具"或"思考→工具"顺序。
	base := 0
	if b0, ok := s.blocks[0]; ok && b0 != nil && b0.kind == "thinking" {
		base = 1
	}
	if s.textEmitted {
		base = 1
		if b0, ok := s.blocks[0]; ok && b0 != nil && b0.kind == "thinking" {
			base = 2
		}
	}
	key := base + tc.Index
	b, ok := s.blocks[key]
	if !ok {
		b = &sseBlock{index: key, kind: "tool_use", toolID: tc.ID, toolName: tc.Function.Name}
		s.blocks[key] = b
	}
	if !b.toolStarted {
		b.toolStarted = true
		if b.toolID == "" {
			b.toolID = fmt.Sprintf("toolu_nvidia_%d", tc.Index)
		}
		fw.writeEvent("content_block_start", contentBlockStartPayload(b.index, "tool_use", b.toolID, b.toolName))
	}
	// OpenAI 流式 tool_calls 的 arguments 是增量字符串，Anthropic 用 input_json_delta 直传
	if tc.Function.Arguments != "" {
		fw.writeEvent("content_block_delta", contentBlockInputJSONDeltaPayload(b.index, tc.Function.Arguments))
	}
}

// closeAll 关闭所有已打开但尚未 closed 的文本/工具/思考块,发出 content_block_stop。
// 对 thinking 块:关块前先发一条空串 signature_delta(无签名上游占位),严格对齐官方序列。
// 已经被 closeThinkingIfOpen/emitThinkingDelta 切换逻辑提前闭合(closed==true)的块跳过,避免重复关门。
// 对只开块却从未下发 thinking_delta 的异常 thinking 块(无推理模型误触发 / 上游异常握手帧):
// 直接丢弃,不发 signature_delta、不发 stop,避免客户端 SDK 收到空 thinking 块报错或卡等。
func (s *sseBlockStates) closeAll(fw sseEventSink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]int, 0, len(s.blocks))
	for k := range s.blocks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		b := s.blocks[k]
		if b.closed {
			continue // 已被切换逻辑提前闭合,不重复关块
		}
		if b.kind == "thinking" && !b.thinkingStarted {
			// 空块丢弃:从未实际下发 thinking_delta 的 thinking 块,当作没开过。
			delete(s.blocks, k)
			continue
		}
		if b.thinkingStarted {
			fw.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(b.index, ""))
			fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
			b.closed = true
		} else if b.textStarted || b.toolStarted {
			fw.writeEvent("content_block_stop", contentBlockStopPayload(b.index))
			b.closed = true
		}
	}
}

// hasEmittedAnyBlock 检查本轮会话是否已发出过至少一个 content_block。
// 注意:从未下发 thinking_delta 的异常 thinking 块(只开块无内容)不计入,避免它错误地
// 抑制空块兜底逻辑,保证无推理模型路径与改动前行为一致。
func (s *sseBlockStates) hasEmittedAnyBlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.blocks {
		if b.thinkingStarted || b.textStarted || b.toolStarted {
			return true
		}
	}
	return false
}

// ensureAtLeastOneBlock 确保流结束前至少发出一个 content_block(若零 Block 则保底发空文本块)
func (s *sseBlockStates) ensureAtLeastOneBlock(fw sseEventSink) {
	if !s.hasEmittedAnyBlock() {
		s.emitTextDelta("", fw)
	}
}

// determineStopReason 根据本轮是否发出过工具块及上游 finishReason 精准计算 Anthropic stop_reason。
// 若包含工具调用，必定返回 "tool_use"，确保 Claude Code 等 Agent 客户端能自动驱动后续工具执行。
func (s *sseBlockStates) determineStopReason(rawFinishReason string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasToolCall {
		return "tool_use"
	}
	switch rawFinishReason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}
