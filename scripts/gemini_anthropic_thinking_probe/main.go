// Command gemini_anthropic_thinking_probe 直连中继分发服务(默认 127.0.0.1:18444),
// 对 antigravity 号池 Gemini 模型(默认 gemini-3.6-flash-high)做"思考内容丢失层定位"的三档对照探测,
// 逐帧打印 SSE,专项核对:Claude Code 走号池 Gemini 时只显示思考标签、思考结束后不显示思考正文,
// 问题到底出在「上游没返回 thought 明文」还是「回译层把思考内容丢了/格式不对」。
//
// 三档对照(同一 prompt 同一模型,差异只在入口/思考注入):
//
//	mode=anthropic       —— 以 Claude Code 姿势 POST /v1/messages(body 带 thinking.type=enabled
//	                          + budget),走 handleAnthropicMessages→TranslateAnthropicToGemini 回译链,
//	                          输出 Anthropic SSE。看 thinking_delta 是否真带思考正文文字、
//	                          content_block_start(thinking) 内容块是否带 signature 字段、
//	                          signature_delta 的 value 是空串还是哨兵。
//	mode=v1raw-thoughts  —— 直发 /v1internal:streamGenerateContent?alt=sse,外层 v1internal 包体内层
//	                          generationConfig.thinkingConfig.includeThoughts=true。这是上游 Gemini 原生 SSE
//	                          (handleV1Internal 对 antigravity provider 注入 includeThoughts 后透传)。
//	                          看 candidates[].content.parts[] 里有没有 thought:true 的 part、其 text 是否非空、
//	                          有没有 thoughtSignature / thoughtSummaryText / thoughtsTokenCount。
//	mode=v1raw-nothoughts—— 同上但不带 includeThoughts,基线对照:确认 thought:true 是 includeThoughts 触发的,
//	                          而非模型默认行为。
//
// 诊断逻辑:
//   - 若 v1raw-thoughts 带回 thought:true 非空 text(且有 thoughtsTokenCount),但 anthropic 档的
//     thinking_delta 文本为空或缺 signature 字段 → 缺失发生在【回译层】(compat_stream.go),走方案 A/B(回退开块/补字段)。
//   - 若 v1raw-thoughts 也不带 thought:true 或 text 为空(只有 thoughtSignature 哨兵帧)→ 缺失发生在
//     【上游/请求注入层】,回译层无错,需查 includeThoughts 是否真注入或模型是否支持。
//   - 若 v1raw-nothoughts 也带回 thought:true → 模型默认就吐思考,includeThoughts 不是关键开关。
//
// 不 import internal/relay(避免拖中继依赖),只复刻协议字段子集。
//
// 链路事实锚定:
//   - Anthropic 入口 internal/relay/compat.go:251 path=="/v1/messages" → handleAnthropicMessages
//     (compat_dispatch.go:193),回译 compat_stream.go handleStreamResponse(apiFormat="anthropic")
//
//   - v1internal 入口 internal/relay/compat.go:281 HasPrefix "/v1internal:" → handleV1Internal
//     (compat_v1internal.go:20),antigravity provider 在 compat_v1internal.go:222 注入
//     generationConfig.thinkingConfig.includeThoughts=true 后透传上游 SSE
//
//   - 鉴权 extractToken(compat_translate_helpers.go:50)认 Authorization Bearer / X-API-Key /
//     ANTHROPIC_API_KEY / API_KEY / x-goog-api-key,故 -key 对两档入口通用
//
// 用法:
//
//	# 本机中继(默认),三档全跑
//	go run ./scripts/gemini_anthropic_thinking_probe -key sk-ant-xxxx
//	# 三档全跑,模型 gemini-3.6-flash-high(默认即是)
//	go run ./scripts/gemini_anthropic_thinking_probe -key sk-ant-xxxx -model gemini-3.6-flash-high
//	# 只跑 Claude Code 回译档
//	go run ./scripts/gemini_anthropic_thinking_probe -key sk-ant-xxxx -modes anthropic
//	# 指向远端中继
//	go run ./scripts/gemini_anthropic_thinking_probe -key sk-ant-xxxx -base-url http://172.16.10.114:18444
//	# 自定义 prompt(逼出思考链)
//	go run ./scripts/gemini_anthropic_thinking_probe -key sk-ant-xxxx -prompt "详述你的推理过程"
//
// API Key 也可用环境变量 RELAY_API_KEY 传入(与 -key 二选一,-key 优先)。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 默认参数(可用 flag 覆盖)。
const (
	// 中继分发服务默认地址:本机 18444。antigravity 号池入口在本机运行时即此地址;
	// 若中继跑在远端可 -base-url 覆盖(如 http://172.16.10.114:18444)。
	defaultBaseURL = "http://127.0.0.1:18444"
	// 用户指定模型:antigravity 号池 Gemini 系,含 flash 关键字 → 回译侧 geminiModelSupportsThinking 命中。
	defaultModel = "gemini-3.6-flash-high"
	// 给真实代码片段让它分析,逼出思考链(与其它探针同一 prompt 思路)。
	defaultPrompt = `分析下面这段 Go 代码的并发安全问题,详述你的推理过程,最后给结论。

func (s *Store) Update(key string, fn func(int) int) {
    s.m.RLock()
    v, ok := s.data[key]
    s.m.RUnlock()
    if !ok { v = 0 }
    nv := fn(v)
    s.m.Lock()
    s.data[key] = nv
    s.m.Unlock()
}
`
	// Anthropic 档的思考预算与正文上限。budget<max 是 Anthropic 官方硬约束(max_tokens 须 > budget_tokens),
	// 且对 claude-* 经 Vertex Anthropic 重译路径同样校验;本探针模型为 gemini-*,走 Gemini 协议无该约束,
	// 但保持 max>budget 的合法形态避免触发任何路径的兜底抬升干扰观测。
	defaultBudgetTokens = 7168
	defaultMaxTokens    = 16384
	// v1internal 默认 project 占位:中继端会自愈,留空也行,这里给显式值便于排查。
	defaultProject = "favorable-synapse-ttvcb"
)

// probeStat 记录一档探测的统计结果,供跨档汇总对照。
type probeStat struct {
	mode                 string
	status               int
	err                  string
	// ===== Anthropic 档统计 =====
	thinkingBlocks       int    // 开过多少个 thinking content_block
	textBlocks           int    // 开过多少个 text content_block
	thinkingDeltaFrames  int    // thinking_delta 帧数
	thinkingDeltaNonEmpty int   // thinking_delta 文本非空的帧数(核心:思考正文是否真下发)
	signatureDeltaFrames int    // signature_delta 帧数
	thinkingBlockHasSig  int    // thinking 块 content_block_start 携带 signature 字段的开块次数
	signatureValueSample string // signature_delta.value 采样(空串 / 哨兵 / 真签名),截断 60
	thinkingText         string // 累积思考正文(anthropic 档),前 600 字打印
	textText             string // 累积正文(anthropic 档)
	// ===== v1internal 档统计(共用 partFieldHits,关键命中单列) =====
	thoughtParts         int    // thought:true part 命中次数
	thoughtTextNonEmpty  int    // thought:true 且 text 非空 part 次数
	thoughtSigParts      int    // thoughtSignature part 命中次数
	thoughtSummParts     int    // thoughtSummaryText part 命中次数
	thoughtsTokenCount   int    // usageMetadata.thoughtsTokenCount 末帧
	finishReason         string
	logPath              string
}

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "中继分发服务地址(默认 http://127.0.0.1:18444)")
	key := flag.String("key", "", "中继 API Key(sk-ant- 前缀);也可用环境变量 RELAY_API_KEY")
	model := flag.String("model", defaultModel, "目标模型名(antigravity 号池 Gemini 简写,如 gemini-3.6-flash-high)")
	prompt := flag.String("prompt", defaultPrompt, "测试 prompt")
	project := flag.String("project", defaultProject, "v1internal 档的 project ID(中继端自愈,默认占位)")
	budget := flag.Int("budget", defaultBudgetTokens, "Anthropic 档 thinking.budget_tokens")
	maxTok := flag.Int("max-tokens", defaultMaxTokens, "Anthropic 档 max_tokens")
	// modes:逗号分隔,默认三档全跑。可只填一档或两档。
	modesStr := flag.String("modes", "anthropic,v1raw-thoughts,v1raw-nothoughts",
		"要跑的档(逗号分隔:anthropic,v1raw-thoughts,v1raw-nothoughts)")
	logDir := flag.String("log-dir", "", "日志文件输出目录(不填则写到 ./logs/)")
	flag.Parse()

	apiKey := strings.TrimSpace(*key)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("RELAY_API_KEY"))
	}
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "[错误] 未提供 API Key。请用 -key sk-ant-xxxx 或设置环境变量 RELAY_API_KEY。\n")
		os.Exit(1)
	}

	modes := parseModes(*modesStr)
	if len(modes) == 0 {
		fmt.Fprintf(os.Stderr, "[错误] -modes 解析为空,允许 anthropic,v1raw-thoughts,v1raw-nothoughts\n")
		os.Exit(1)
	}

	// ===== tee: 每档一个独立日志文件,屏幕也实时输出 =====
	summaryPath := buildLogPath(*logDir, "summary")
	realStdout := dupStdout()
	startTee(realStdout, summaryPath)

	fmt.Printf("==== Antigravity 号池 Gemini 思考内容丢失层定位探针 ====\n")
	fmt.Printf("BaseURL  : %s\n", *baseURL)
	fmt.Printf("Model    : %s\n", *model)
	fmt.Printf("Project  : %s (仅 v1internal 档用)\n", *project)
	fmt.Printf("Modes    : %v\n", modes)
	fmt.Printf("Budget   : %d | MaxTokens: %d (仅 anthropic 档用)\n", *budget, *maxTok)
	fmt.Printf("Prompt   : %q\n", *prompt)
	fmt.Printf("Key      : %s...\n", safePrefix(apiKey, 14))
	fmt.Printf("总览日志 : %s (跨档汇总写这里)\n", summaryPath)
	fmt.Printf("分档日志 : 每档单独一个文件,见下方各档输出\n\n")

	var stats []*probeStat
	for _, mode := range modes {
		fmt.Printf("######## 档 mode=%s ########\n", mode)
		modeLog := buildLogPath(*logDir, "mode_"+mode)
		st := &probeStat{mode: mode, status: -1, logPath: modeLog}
		stats = append(stats, st)
		runOnceMode(*baseURL, apiKey, *model, *project, *prompt, *budget, *maxTok, mode, modeLog, st)
		fmt.Println()
	}

	// ===== 跨档汇总 =====
	fmt.Println("==================== 思考内容跨档汇总 ====================")
	printSummary(stats)
	fmt.Println("=========================================================")
	printDiagnosis(stats)

	flushTee()
}

// parseModes 解析 -modes 逗号分隔字符串,去空白去重保序,仅允许三档名。
func parseModes(s string) []string {
	allowed := map[string]bool{"anthropic": true, "v1raw-thoughts": true, "v1raw-nothoughts": true}
	seen := map[string]bool{}
	var out []string
	for _, m := range strings.Split(s, ",") {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if !allowed[m] {
			fmt.Fprintf(os.Stderr, "[错误] 未知档名 %q (允许 anthropic,v1raw-thoughts,v1raw-nothoughts)\n", m)
			return nil
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// runOnceMode 按档分发到对应执行函数。
func runOnceMode(baseURL, apiKey, model, project, prompt string, budget, maxTok int, mode, modeLog string, stat *probeStat) {
	stopTee := startModeTee(modeLog)
	defer stopTee()

	switch mode {
	case "anthropic":
		runAnthropicMode(baseURL, apiKey, model, prompt, budget, maxTok, stat)
	case "v1raw-thoughts", "v1raw-nothoughts":
		includeThoughts := mode == "v1raw-thoughts"
		runV1RawMode(baseURL, apiKey, model, project, prompt, includeThoughts, stat)
	}
}

// ===== Anthropic 档:以 Claude Code 姿势 POST /v1/messages,看回译后 Anthropic SSE =====

// AnthropicRequestBody 以 Anthropic Messages 协议构造,带 thinking.type=enabled + budget,stream=true。
// 对齐 Claude Code 真实请求形态,触发 compat_dispatch.go:handleAnthropicMessages →
// TranslateAnthropicToGemini 注入 thinkingConfig.includeThoughts=true → 回译 Anthropic SSE。
type AnthropicRequestBody struct {
	Model       string                 `json:"model"`
	Messages    []AnthropicMessageBody `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Thinking    AnthropicThinkingBody  `json:"thinking"`
	Stream      bool                   `json:"stream"`
}

type AnthropicMessageBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AnthropicThinkingBody 对齐 AnthropicThinking(compat_translate_types_anthropic.go:118):
// type=enabled 触发回译侧注入 includeThoughts;budget_tokens 透传 thinkingBudget。
type AnthropicThinkingBody struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// runAnthropicMode 构造 Anthropic 请求体,POST /v1/messages,逐帧扫描回译后 Anthropic SSE。
func runAnthropicMode(baseURL, apiKey, model, prompt string, budget, maxTok int, stat *probeStat) {
	body := &AnthropicRequestBody{
		Model:     model,
		Messages:  []AnthropicMessageBody{{Role: "user", Content: prompt}},
		MaxTokens: maxTok,
		Thinking:  AnthropicThinkingBody{Type: "enabled", BudgetTokens: budget},
		Stream:    true,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		stat.err = "构造请求体失败: " + err.Error()
		fmt.Printf("[构造请求体失败] %v\n", err)
		return
	}

	targetURL := strings.TrimRight(baseURL, "/") + "/v1/messages"
	fmt.Printf("[档] %s | 入口 /v1/messages(Claude Code 回译链)\n", stat.mode)
	fmt.Printf("[目标] %s\n", targetURL)
	fmt.Printf("[请求体] 完整 JSON: %s\n\n", string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		stat.err = "构造请求失败: " + err.Error()
		fmt.Printf("[构造请求失败] %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// Anthropic SDK 走 x-api-key + anthropic-version;两条都带兼容 extractToken 与上游转发。
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	client := &http.Client{Timeout: 0} // 流式无读超时,避免长输出被掐断

	resp, err := client.Do(req)
	if err != nil {
		stat.err = "中继请求失败: " + err.Error()
		fmt.Printf("[中继请求失败] 已耗时 %v | %v\n", time.Since(start), err)
		return
	}
	defer resp.Body.Close()

	stat.status = resp.StatusCode
	fmt.Printf("[响应状态码] %d | 发起后 %v\n", resp.StatusCode, time.Since(start))
	fmt.Printf("[响应头 Content-Type] %q\n", resp.Header.Get("Content-Type"))
	fmt.Printf("[响应头 X-Accel-Buffering] %q\n", resp.Header.Get("X-Accel-Buffering"))

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		errStr := truncate(string(errBytes), 2000)
		stat.err = fmt.Sprintf("中继 %d: %s", resp.StatusCode, truncate(string(errBytes), 80))
		fmt.Printf("[中继非 200 错误体,mode=%s]\n%s\n", stat.mode, errStr)
		return
	}

	// 流式逐行扫描: 8MB 单行缓冲,避免长帧被截断。
	fmt.Printf("[流式] mode=%s 逐行读取 Anthropic SSE, 带前缀 [行号 | ms | cumB]:\n", stat.mode)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	totalBytes := 0
	curEvent := "" // 当前 event: 行的事件名,与紧随其后的 data: 行配对
	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		totalBytes += len(line) + 1
		fmt.Printf("  [#%-4d | %6dms | cum=%dB] %s\n", lineNo, time.Since(start).Milliseconds(), totalBytes, line)

		if line == "" {
			curEvent = "" // SSE 事件分隔,重置当前 event 名
			continue
		}
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			fmt.Printf("  ↑ 收到 [DONE],mode=%s 流结束\n", stat.mode)
			break
		}
		// data 行配 event 名优先用 curEvent,空则从 data JSON 的 type 字段取(容忍无 event: 行的实现)。
		evtType := curEvent
		if evtType == "" {
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(data), &probe) == nil {
				evtType = probe.Type
			}
		}
		parseAnthropicFrame(data, evtType, stat)
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("[流式扫描出错] %v\n", err)
	}

	fmt.Println("---- mode=" + stat.mode + " Anthropic 回译汇总 ----")
	fmt.Printf("总行数             : %d\n", lineNo)
	fmt.Printf("累计字节           : %d\n", totalBytes)
	fmt.Printf("总耗时             : %v\n", time.Since(start))
	fmt.Printf("thinking blocks    : %d\n", stat.thinkingBlocks)
	fmt.Printf("text blocks         : %d\n", stat.textBlocks)
	fmt.Printf("thinking_delta帧   : %d (其中文本非空: %d)\n", stat.thinkingDeltaFrames, stat.thinkingDeltaNonEmpty)
	fmt.Printf("signature_delta帧   : %d\n", stat.signatureDeltaFrames)
	fmt.Printf("thinking块带sig字段: %d / %d (开块次数中携带 signature 字段的比例)\n",
		stat.thinkingBlockHasSig, stat.thinkingBlocks)
	fmt.Printf("signature采样值    : %q\n", stat.signatureValueSample)
	if stat.thinkingText == "" {
		fmt.Printf("累积思考正文        : (空 —— 思考内容未随 thinking_delta 下发!)\n")
	} else {
		fmt.Printf("累积思考正文(前 600 字):\n%s\n", truncate(stat.thinkingText, 600))
	}
	if stat.textText != "" {
		fmt.Printf("累积正文(前 600 字):\n%s\n", truncate(stat.textText, 600))
	}
}

// parseAnthropicFrame 解析 Anthropic 回译帧,统计 thinking/text 块与 delta,提取 signature 诊断字段。
func parseAnthropicFrame(data, evtType string, stat *probeStat) {
	switch evtType {
	case "content_block_start":
		var fr struct {
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
		}
		if json.Unmarshal([]byte(data), &fr) != nil || len(fr.ContentBlock) == 0 {
			return
		}
		var cb map[string]json.RawMessage
		if json.Unmarshal(fr.ContentBlock, &cb) != nil {
			return
		}
		var cbType string
		if raw, ok := cb["type"]; ok {
			_ = json.Unmarshal(raw, &cbType)
		}
		if cbType == "thinking" {
			stat.thinkingBlocks++
			// 关键诊断:thinking 开块 content_block 是否携带 signature 字段(及其值)。
			keys := sortedKeys(cb)
			fmt.Printf("  ✓ content_block_start(thinking) index=%d 字段: [%s]\n", fr.Index, strings.Join(keys, ", "))
			for _, k := range keys {
				fmt.Printf("      · %s = %s\n", k, string(cb[k]))
			}
			if _, hasSig := cb["signature"]; hasSig {
				stat.thinkingBlockHasSig++
			}
		} else if cbType == "text" {
			stat.textBlocks++
			fmt.Printf("  ✓ content_block_start(text) index=%d\n", fr.Index)
		}
	case "content_block_delta":
		var fr struct {
			Index int             `json:"index"`
			Delta json.RawMessage `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &fr) != nil || len(fr.Delta) == 0 {
			return
		}
		var d struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Text      string `json:"text"`
			Signature string `json:"signature"`
		}
		if json.Unmarshal(fr.Delta, &d) != nil {
			return
		}
		switch d.Type {
		case "thinking_delta":
			stat.thinkingDeltaFrames++
			if d.Thinking != "" {
				stat.thinkingDeltaNonEmpty++
				stat.thinkingText += d.Thinking
				fmt.Printf("  ✓ thinking_delta(index=%d) 非空(%d字): %q\n", fr.Index, len(d.Thinking), truncate(d.Thinking, 120))
			} else {
				fmt.Printf("  ⚠️ thinking_delta(index=%d) 空串(思考内容未下发!)\n", fr.Index)
			}
		case "signature_delta":
			stat.signatureDeltaFrames++
			if stat.signatureValueSample == "" {
				stat.signatureValueSample = truncate(d.Signature, 60)
			}
			if d.Signature == "" {
				fmt.Printf("  ✓ signature_delta(index=%d) 空串(官方 omitted 占位形态)\n", fr.Index)
			} else {
				fmt.Printf("  ✓ signature_delta(index=%d) value=%q\n", fr.Index, truncate(d.Signature, 60))
			}
		case "text_delta":
			if d.Text != "" {
				stat.textText += d.Text
			}
		case "input_json_delta":
			// tool_use 增量,不在思考诊断范围,静默
		}
	case "message_delta":
		var fr struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &fr) == nil {
			fmt.Printf("  ↑ message_delta stop_reason=%q usage: in=%d out=%d\n",
				fr.Delta.StopReason, fr.Usage.InputTokens, fr.Usage.OutputTokens)
		}
	case "message_stop":
		fmt.Printf("  ↑ message_stop 流终止\n")
	}
}

// ===== v1internal 档:直发 /v1internal:streamGenerateContent,看上游 Gemini 原生 SSE =====

// V1InternalRequest 对应 v1internal 外层包体:project/requestId/model/request。
// 与 compat_v1internal.go:211 解析的 v1internalReq 同名同结构(子集)。
type V1InternalRequest struct {
	Project   string                `json:"project"`
	RequestID string                `json:"requestId"`
	Model     string                `json:"model"`
	Request   GeminiGenerateRequest `json:"request"`
}

// GeminiGenerateRequest 对应 v1internal 内层标准 Gemini 请求对象。
type GeminiGenerateRequest struct {
	Contents        []GeminiContent        `json:"contents"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

// GeminiGenerationConfig: includeThoughts 决定档(v1raw-thoughts true / v1raw-nothoughts false 或省略)。
type GeminiGenerationConfig struct {
	ThinkingConfig *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// GeminiThinkingConfig 仅 includeThoughts,不写 thinkingBudget(避免对已含默认预算的模型触发 400),
// 与 compat_v1internal.go:228 注入形态一致(只 includeThoughts:true)。
type GeminiThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts"`
}

// runV1RawMode 构造 v1internal 请求体(includeThoughts 控制开关),POST streamGenerateContent,逐帧扫描上游 Gemini SSE。
func runV1RawMode(baseURL, apiKey, model, project, prompt string, includeThoughts bool, stat *probeStat) {
	v1Req := &V1InternalRequest{
		Project:   project,
		RequestID: fmt.Sprintf("chat/probe-%d", timeNowUnixMilliSafe()),
		Model:     model,
		Request: GeminiGenerateRequest{
			Contents: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: prompt}}},
			},
		},
	}
	if includeThoughts {
		v1Req.Request.GenerationConfig = &GeminiGenerationConfig{
			ThinkingConfig: &GeminiThinkingConfig{IncludeThoughts: true},
		}
	}
	bodyBytes, err := json.Marshal(v1Req)
	if err != nil {
		stat.err = "构造请求体失败: " + err.Error()
		fmt.Printf("[构造请求体失败] %v\n", err)
		return
	}

	targetURL := strings.TrimRight(baseURL, "/") + "/v1internal:streamGenerateContent?alt=sse"
	fmt.Printf("[档] %s | 入口 /v1internal:streamGenerateContent(includeThoughts=%v)\n", stat.mode, includeThoughts)
	fmt.Printf("[目标] %s\n", targetURL)
	fmt.Printf("[请求体] 完整 JSON: %s\n\n", string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		stat.err = "构造请求失败: " + err.Error()
		fmt.Printf("[构造请求失败] %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	client := &http.Client{Timeout: 0}

	resp, err := client.Do(req)
	if err != nil {
		stat.err = "中继请求失败: " + err.Error()
		fmt.Printf("[中继请求失败] 已耗时 %v | %v\n", time.Since(start), err)
		return
	}
	defer resp.Body.Close()

	stat.status = resp.StatusCode
	fmt.Printf("[响应状态码] %d | 发起后 %v\n", resp.StatusCode, time.Since(start))
	fmt.Printf("[响应头 Content-Type] %q\n", resp.Header.Get("Content-Type"))

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		errStr := truncate(string(errBytes), 2000)
		stat.err = fmt.Sprintf("中继 %d: %s", resp.StatusCode, truncate(string(errBytes), 80))
		fmt.Printf("[中继非 200 错误体,mode=%s]\n%s\n", stat.mode, errStr)
		return
	}

	// 流式逐行扫描上游 Gemini 原生 SSE。
	fmt.Printf("[流式] mode=%s 逐行读取 Gemini 原生 SSE, 带前缀 [行号 | ms | cumB]:\n", stat.mode)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	totalBytes := 0
	partFieldHits := map[string]int{}
	var thinkingTextBuf strings.Builder
	var textBuf strings.Builder
	var lastUsage *GeminiUsageMeta

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		totalBytes += len(line) + 1
		fmt.Printf("  [#%-4d | %6dms | cum=%dB] %s\n", lineNo, time.Since(start).Milliseconds(), totalBytes, line)

		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "data: ") {
			continue
		}
		var dataStr string
		if strings.HasPrefix(line, "data: ") {
			dataStr = strings.TrimPrefix(line, "data: ")
		} else {
			dataStr = strings.TrimPrefix(line, "data:")
		}
		dataStr = strings.TrimSpace(dataStr)
		if dataStr == "[DONE]" {
			fmt.Printf("  ↑ 收到 [DONE],mode=%s 流结束\n", stat.mode)
			break
		}

		// 解析 v1internal 上游 SSE chunk: {"response":{"candidates":[...],"usageMetadata":{...}},"traceId":"..."}
		var chunk V1InternalStreamChunk
		if json.Unmarshal([]byte(dataStr), &chunk) != nil {
			fmt.Printf("  [ warn] data 帧解析失败,原样保留: %s\n", truncate(dataStr, 200))
			continue
		}

		if chunk.Response.UsageMetadata != nil {
			lastUsage = chunk.Response.UsageMetadata
			fmt.Printf("  ↑ 本帧 usageMetadata: prompt=%d candidates=%d thoughts=%d total=%d\n",
				lastUsage.PromptTokenCount, lastUsage.CandidatesTokenCount, lastUsage.ThoughtsTokenCount, lastUsage.TotalTokenCount)
		}

		for ci, cand := range chunk.Response.Candidates {
			if cand.FinishReason != "" {
				stat.finishReason = cand.FinishReason
				fmt.Printf("  ↑ candidate[%d] finishReason=%s\n", ci, cand.FinishReason)
			}
			if len(cand.Content.Parts) == 0 {
				continue
			}
			for pi, part := range cand.Content.Parts {
				// part.Dump 是 part 原始 JSON 对象,穷举其所有 key,不预设字段名。
				var partMap map[string]json.RawMessage
				keys := []string{}
				if len(part.Dump) > 0 && string(part.Dump) != "{}" {
					if json.Unmarshal(part.Dump, &partMap) == nil {
						for k := range partMap {
							keys = append(keys, k)
						}
					}
				}
				sort.Strings(keys)
				thinkers := map[string]bool{
					"thought":            true,
					"thoughtSignature":   true,
					"thoughtSummaryText": true,
				}
				var tags []string
				isThought := false
				thoughtTextNonEmpty := false
				for _, k := range keys {
					partFieldHits[k]++
					if thinkers[k] {
						tags = append(tags, k)
					}
					// k=="thought" 是 bool:true 标记;k=="text" 是该 part 的文字内容(thought:true 时即思考正文)。
					if k == "thought" {
						var b bool
						if json.Unmarshal(partMap[k], &b) == nil && b {
							isThought = true
						}
					}
					if k == "text" {
						var s string
						if json.Unmarshal(partMap[k], &s) == nil {
							if s != "" {
								thoughtTextNonEmpty = true
							}
							if isThought {
								thinkingTextBuf.WriteString(s)
							} else {
								textBuf.WriteString(s)
							}
						}
					}
				}
				prefix := fmt.Sprintf("  ✓ candidate[%d].parts[%d] 字段: [%s]", ci, pi, strings.Join(keys, ", "))
				if len(tags) > 0 {
					prefix += "  <<< 思考标记命中: " + strings.Join(tags, ", ") + " >>>"
				}
				fmt.Println(prefix)
				for _, k := range keys {
					raw := string(partMap[k])
					if thinkers[k] || k == "text" {
						fmt.Printf("      · %s = %s\n", k, truncate(raw, 200))
					} else {
						fmt.Printf("      · %s = %s\n", k, truncate(raw, 80))
					}
				}
				if isThought {
					stat.thoughtParts++
					if thoughtTextNonEmpty {
						stat.thoughtTextNonEmpty++
					}
				}
				if _, ok := partMap["thoughtSignature"]; ok {
					stat.thoughtSigParts++
				}
				if _, ok := partMap["thoughtSummaryText"]; ok {
					stat.thoughtSummParts++
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("[流式扫描出错] %v\n", err)
	}

	if lastUsage != nil {
		stat.thoughtsTokenCount = lastUsage.ThoughtsTokenCount
	}

	fmt.Println("---- mode=" + stat.mode + " 上游 Gemini 原生汇总 ----")
	fmt.Printf("总行数             : %d\n", lineNo)
	fmt.Printf("累计字节           : %d\n", totalBytes)
	fmt.Printf("总耗时             : %v\n", time.Since(start))
	fmt.Printf("finishReason       : %q\n", stat.finishReason)
	if lastUsage != nil {
		fmt.Printf("末帧 usage         : prompt=%d candidates=%d thoughts=%d total=%d\n",
			lastUsage.PromptTokenCount, lastUsage.CandidatesTokenCount, lastUsage.ThoughtsTokenCount, lastUsage.TotalTokenCount)
	} else {
		fmt.Printf("末帧 usage         : (未命中)\n")
	}
	fmt.Println("---- mode=" + stat.mode + " part 字段命中统计 ----")
	if len(partFieldHits) == 0 {
		fmt.Println("无任何 part 字段命中,上游可能没流式吐 delta 或空响应 → ✗")
	} else {
		type fieldHit struct {
			name  string
			count int
		}
		var hits []fieldHit
		for k, c := range partFieldHits {
			hits = append(hits, fieldHit{k, c})
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].count > hits[j].count })
		for _, h := range hits {
			mark := ""
			switch h.name {
			case "text":
				mark = "(文字)"
			case "thought":
				mark = "(思考标记 ✓)"
			case "thoughtSignature":
				mark = "(思考签名)"
			case "thoughtSummaryText":
				mark = "(思考摘要文本)"
			}
			fmt.Printf("  %-20s 出现 %d 次 %s\n", h.name, h.count, mark)
		}
	}
	fmt.Printf("thought:true part  : %d (其中 text 非空: %d)\n", stat.thoughtParts, stat.thoughtTextNonEmpty)
	fmt.Printf("thoughtSignature   : %d | thoughtSummaryText: %d\n", stat.thoughtSigParts, stat.thoughtSummParts)
	if t := thinkingTextBuf.String(); t != "" {
		fmt.Printf("累积 thought 思考文本(前 600 字):\n%s\n", truncate(t, 600))
	} else {
		fmt.Printf("累积 thought 思考文本: (空)\n")
	}
	if t := textBuf.String(); t != "" {
		fmt.Printf("累积正文文本(前 600 字):\n%s\n", truncate(t, 600))
	}
}

// ===== v1internal 上游响应结构体子集(Gemini 原生格式) =====

type V1InternalStreamChunk struct {
	Response GeminiResponse `json:"response"`
	TraceID  string         `json:"traceId"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata  *GeminiUsageMeta  `json:"usageMetadata"`
	ModelVersion  string            `json:"modelVersion"`
}

type GeminiCandidate struct {
	Content      GeminiContentBody `json:"content"`
	FinishReason string            `json:"finishReason"`
}

type GeminiContentBody struct {
	Role  string           `json:"role"`
	Parts []GeminiPartDump `json:"parts"`
}

// GeminiPartDump 用 RawMessage 承载 part,以便穷举所有 key(text/thought/thoughtSignature/thoughtSummaryText 等)。
type GeminiPartDump struct {
	Dump json.RawMessage `json:"-"`
}

func (p *GeminiPartDump) UnmarshalJSON(data []byte) error {
	p.Dump = make([]byte, len(data))
	copy(p.Dump, data)
	return nil
}

type GeminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

// ===== 汇总与诊断 =====

func printSummary(stats []*probeStat) {
	fmt.Printf("%-18s %-7s %-9s %-9s %-20s %-16s %s\n",
		"Mode", "Status", "thinkBlk", "thinkDelta",
		"thoughtPart(nonEmpty)", "thoughtSigPart", "判定")
	for _, st := range stats {
		mark := "✗ "
		switch st.mode {
		case "anthropic":
			if st.err != "" {
				mark += "报错:" + truncate(st.err, 18)
			} else if st.thinkingDeltaNonEmpty > 0 {
				mark += "回译下发思考正文 ✓"
			} else if st.thinkingBlocks > 0 {
				mark += "只有思考块无正文 ⚠️"
			} else {
				mark += "无思考块"
			}
		case "v1raw-thoughts", "v1raw-nothoughts":
			if st.err != "" {
				mark += "报错:" + truncate(st.err, 18)
			} else if st.thoughtTextNonEmpty > 0 {
				mark += "上游返回思考正文 ✓"
			} else if st.thoughtParts > 0 {
				mark += "thought:true 但 text 空 ⚠️"
			} else {
				mark += "无 thought:true"
			}
		}
		fmt.Printf("%-18s %-7d %-9d %-9d %-20d %-16d %s\n",
			st.mode, st.status, st.thinkingBlocks, st.thinkingDeltaNonEmpty,
			st.thoughtTextNonEmpty, st.thoughtSigParts, mark)
		fmt.Printf("           日志: %s\n", st.logPath)
	}
}

// printDiagnosis 据三档数据输出丢失层定位结论。
func printDiagnosis(stats []*probeStat) {
	get := func(mode string) *probeStat {
		for _, st := range stats {
			if st.mode == mode {
				return st
			}
		}
		return nil
	}
	anthr := get("anthropic")
	raw := get("v1raw-thoughts")
	noRaw := get("v1raw-nothoughts")

	fmt.Println("\n==================== 丢失层定位诊断 ====================")
	if anthr != nil && raw != nil {
		upstreamHasText := raw.thoughtTextNonEmpty > 0
		anthrHasText := anthr.thinkingDeltaNonEmpty > 0
		fmt.Printf("[上游事实] v1raw-thoughts 档 thought:true 且 text 非空 part = %d\n", raw.thoughtTextNonEmpty)
		fmt.Printf("[回译事实] anthropic 档 thinking_delta 文本非空帧 = %d\n", anthr.thinkingDeltaNonEmpty)
		fmt.Printf("[开块诊断] anthropic 档 thinking 块开块携带 signature 字段 = %d / %d\n",
			anthr.thinkingBlockHasSig, anthr.thinkingBlocks)
		fmt.Printf("[签名采样] anthropic 档 signature_delta.value = %q\n", anthr.signatureValueSample)

		switch {
		case !upstreamHasText && anthrHasText:
			fmt.Println("结论: 上游不返思考正文但回译层却下发 → 不可能,请复核数据(可能输入了错模型)")
		case upstreamHasText && !anthrHasText:
			fmt.Println("结论: 【回译层丢失】上游确有 thought 明文思考,但 Anthropic 回译链(compat_stream.go)")
			fmt.Println("      的 thinking_delta 未把思考正文下发(或开块 content_block 缺 signature 字段导致")
			fmt.Println("      Claude Code 的 MessageAccumulator 丢弃整块)。修复聚焦 compat_stream.go:")
			fmt.Println("      ① 开块补回 signature:\"\";② 确认 thinking_delta 推 cleanText(part.Text 经")
			fmt.Println("      SanitizeAllThoughtSignatures 清洗后非空)。走方案 A/B 待二选一。")
		case upstreamHasText && anthrHasText:
			fmt.Println("结论: 【回译层正常】上游有思考正文且回译层也下发了。问题在 Claude Code 客户端侧")
			fmt.Println("      或 signature_delta 的 value 非空串导致 SDK 校验失败。检查 anthropic 档日志")
			fmt.Println("      signature 采样值:若为哨兵 skip_thought_signature_validator 而非空串,")
			fmt.Println("      Claude Code 可能因签名非真签名拒绝渲染思考 → 考虑把 signature_delta 改回空串。")
		case !upstreamHasText && !anthrHasText:
			fmt.Println("结论: 【上游/注入层缺失】上游 v1internal 也没返回 thought:true 正文,回译层无错。")
			fmt.Println("      排查:includeThoughts 是否真注入(看响应有 thoughtSignature 哨兵帧?),")
			fmt.Println("      或该模型 gemini-3.6-flash-high 是否真支持思考(换 gemini-2.5-flash 对照)。")
		}
	}

	if raw != nil && noRaw != nil {
		fmt.Println()
		fmt.Printf("[includeThoughts 对照] thoughts 档=%d, nothoughts 档=%d\n",
			raw.thoughtTextNonEmpty, noRaw.thoughtTextNonEmpty)
		if raw.thoughtTextNonEmpty > 0 && noRaw.thoughtTextNonEmpty == 0 {
			fmt.Println("结论: includeThoughts:true 是产生思考正文的关键开关(关掉就没)→ 与 compat_v1internal.go:222 注入一致。")
		} else if noRaw.thoughtTextNonEmpty > 0 {
			fmt.Println("结论: nothoughts 档也返回思考正文 → 模型默认就吐思考,includeThoughts 非关键开关。")
		} else {
			fmt.Println("结论: 两档都没思考正文 → 可能模型本身不吐思考或请求体被中继改写,查日志错误体。")
		}
	}
	fmt.Println("=========================================================")
}

// ===== 工具函数 =====

// sortedKeys 返回 map 的排序后 key 列表。
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func buildLogPath(dir, part string) string {
	if dir == "" {
		dir = filepath.Join(".", "logs")
	}
	stamp := timeNowStampSafe()
	name := "gemini_thinking_probe_" + stamp
	if part != "" {
		name += "_" + part
	}
	name += ".log"
	return filepath.Join(dir, name)
}

func timeNowStampSafe() string {
	return time.Now().Format("20060102_150405")
}

func timeNowUnixMilliSafe() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func dupStdout() *os.File {
	return os.Stdout
}

// ===== tee 实现(总览 + 分档,互不干扰) =====

var (
	summaryWriter  *os.File
	summaryLogPath string
	summaryScreen  *os.File
)

func startTee(realStdout *os.File, logFile string) {
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		fmt.Fprintf(realStdout, "[warn] 建日志父目录失败,仅输出到屏幕: %v\n", err)
		return
	}
	f, err := os.Create(logFile)
	if err != nil {
		fmt.Fprintf(realStdout, "[warn] 创建日志文件失败(%s): %v,仅输出到屏幕\n", logFile, err)
		return
	}
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(realStdout, "[warn] os.Pipe 失败,退化为仅写文件: %v\n", err)
		_ = f.Close()
		return
	}
	os.Stdout = w
	mw := io.MultiWriter(realStdout, f)
	go func() {
		_, _ = io.Copy(mw, r)
		_ = f.Close()
		_ = r.Close()
	}()
	fmt.Printf("[log] 本运行总览日志将写入: %s\n", logFile)
	summaryWriter = w
	summaryLogPath = logFile
	summaryScreen = realStdout
}

func startModeTee(logPath string) func() {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(os.Stdout, "[warn] 建分档日志父目录失败(%s): %v\n", logPath, err)
		return func() {}
	}
	f, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stdout, "[warn] 创建分档日志失败(%s): %v\n", logPath, err)
		return func() {}
	}
	savedOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stdout, "[warn] os.Pipe 失败,本档日志可能无法分流: %v\n", err)
		_ = f.Close()
		return func() {}
	}
	os.Stdout = w
	mw := io.MultiWriter(savedOut, f)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(mw, r)
		_ = f.Close()
		_ = r.Close()
		close(done)
	}()
	return func() {
		os.Stdout = savedOut
		_ = w.Close()
		<-done
		fmt.Fprintf(os.Stdout, "[log] 分档日志已写入: %s\n", logPath)
	}
}

func flushTee() {
	if summaryWriter == nil {
		return
	}
	_ = summaryWriter.Close()
	summaryWriter = nil
	if summaryScreen != nil {
		os.Stdout = summaryScreen
	}
	fmt.Fprintf(os.Stderr, "[log] 总览日志已写入: %s\n", summaryLogPath)
}

var _ = io.EOF
