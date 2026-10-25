package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	opencodeSessionCounter uint64
	opencodeSessionLastTs  int64
	opencodeSessionMu      sync.Mutex
	opencodeSessionRe      = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
)

// patchResponsesBodyModel 替换 Responses API 请求体的顶层 model 字段。
//
// 与 patchRoutedBodyModel 的差异: 后者用 map[string]json.RawMessage 保留所有字段,
// 本函数同样保留全部字段(含 input/tools/max_output_tokens/store/include 等 Responses
// 专有字段), 仅覆盖 model。返回 (新body, 是否成功); 解析失败时返回 (nil, false),
// 由调用方回退原始 body。
func patchResponsesBodyModel(body []byte, model string) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, false
	}
	mb, err := json.Marshal(model)
	if err != nil {
		return nil, false
	}
	obj["model"] = mb
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// isOpenCodeResponsesOnlyModel 判定模型是否仅支持 OpenAI Responses API。
//
// 实测(2026-09-27, 对 Zen /v1/models 全量免费模型逐个打两个端点):
//   - muse-spark-1.2-contributor-free / muse-spark-1.3-contributor-free:
//     POST /chat/completions → 400 {"type":"ModelProtocolUnsupported"}
//     POST /responses        → 200 (需带完整 tools, 否则 403 FreeTierError)
//   - jev-1.13-free: 两个端点均 400/403, 上游未开放, 不在此列(避免无效转发)。
//
// 命中该判定的模型, 中继需把上游端点从 /chat/completions 切到 /responses,
// 并保持 Responses 协议透传(不做 Chat 转换), 否则必然 400。
func isOpenCodeResponsesOnlyModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	// muse-spark 系列: 实测 1.2 / 1.3 均仅支持 Responses API。
	return strings.HasPrefix(name, "muse-spark-")
}

// isOpenCodeFreeModel 判定模型是否为 OpenCode Zen 免费额度模型。
//
// 判定依据(2026-09-27 对 Zen /v1/models 与实测):
//   - 免费模型 id 统一以 "-free" 结尾(如 mimo-v2.5-free / ling-3.0-flash-fin-free);
//   - big-pickle 为免费但无 -free 后缀, 需显式列出。
//
// 采用后缀判定而非硬编码全量清单: Zen 会持续新增免费模型(如 jev-1.13-free /
// longcat-2.5-preview-free / space-bunny-free 均晚于代码内 OpenCodeSupportedModels
// 清单出现), 硬编码清单会漏判导致这些模型在非流式请求下被上游 403。
//
// 该判定的唯一用途: 免费模型仅接受 stream=true, 故非流式请求需强制走上游流式
// 并在本地聚合(见 opencode_sse_aggregate.go)。付费模型不受此限, 保持原行为。
func isOpenCodeFreeModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	// 剥离可能存在的 "opencode/" 前缀与变体后缀, 兼容 clientModel 形态传入。
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	if name == "big-pickle" {
		return true
	}
	return strings.HasSuffix(name, "-free")
}

// generateOpenCodeSessionID 生成符合 OpenCode Canonical 规范的会话 ID (ses_ + 12 hex + 14 Base62)。
func generateOpenCodeSessionID() string {
	opencodeSessionMu.Lock()
	now := time.Now().UnixMilli()
	if now != opencodeSessionLastTs {
		opencodeSessionLastTs = now
		opencodeSessionCounter = 0
	}
	opencodeSessionCounter++
	cnt := opencodeSessionCounter
	opencodeSessionMu.Unlock()

	current := (uint64(now) * 0x1000) + (cnt & 0x0FFF)
	val := ^current

	var timeBytes [6]byte
	for i := 0; i < 6; i++ {
		shift := 40 - 8*i
		timeBytes[i] = byte((val >> shift) & 0xFF)
	}
	timeHex := hex.EncodeToString(timeBytes[:])

	var randBytes [14]byte
	_, _ = rand.Read(randBytes[:])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for _, b := range randBytes {
		randBase62.WriteByte(opencodeBase62Chars[int(b)%62])
	}

	return fmt.Sprintf("ses_%s%s", timeHex, randBase62.String())
}

// generateOpenCodeRequestID 生成符合 OpenCode Canonical 规范的请求 ID (msg_ + 12 hex + 14 Base62)。
func generateOpenCodeRequestID() string {
	now := time.Now().UnixMilli()
	current := (uint64(now) * 0x1000) + 1
	var timeBytes [6]byte
	for i := 0; i < 6; i++ {
		shift := 40 - 8*i
		timeBytes[i] = byte((current >> shift) & 0xFF)
	}
	timeHex := hex.EncodeToString(timeBytes[:])

	var randBytes [14]byte
	_, _ = rand.Read(randBytes[:])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for _, b := range randBytes {
		randBase62.WriteByte(opencodeBase62Chars[int(b)%62])
	}

	return fmt.Sprintf("msg_%s%s", timeHex, randBase62.String())
}

// translateToOpenCodeSession 将下游客户端的会话标识映射为合法的 OpenCode Canonical 格式。
func translateToOpenCodeSession(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if opencodeSessionRe.MatchString(trimmed) {
		return trimmed
	}
	h := sha256.Sum256([]byte("opencode\x00generic\x00" + trimmed))
	timeHex := hex.EncodeToString(h[:6])
	var randBase62 strings.Builder
	randBase62.Grow(14)
	for i := 6; i < 20; i++ {
		randBase62.WriteByte(opencodeBase62Chars[int(h[i])%62])
	}
	return fmt.Sprintf("ses_%s%s", timeHex, randBase62.String())
}
