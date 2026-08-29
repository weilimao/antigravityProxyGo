// Command nvidia_tool_call_probe 用于对比不同模型在 NVIDIA NIM 上游发起工具调用时的流式时序差异与深度思考链行为。
// 目标:量化模型在复杂任务下的长思考耗时、工具参数生成时序，以及流式返回时客户端视角的静默卡顿窗口。
//
// 本脚本直连 https://integrate.api.nvidia.com/v1/chat/completions,绕开代理,
// 实时打印上游 SSE 真实时序、思考链(reasoning/thinking)流式打字过程及工具调用生成。
//
// 用法:
//
//	go run ./scripts/nvidia_tool_call_probe                                    # 默认跑 kimi-k3 (max 思考等级)
//	go run ./scripts/nvidia_tool_call_probe -models "moonshotai/kimi-k3,meta/llama-3.3-70b-instruct"
//	go run ./scripts/nvidia_tool_call_probe -models "deepseek-ai/deepseek-v4-pro-0813"
//	go run ./scripts/nvidia_tool_call_probe -effort high                      # 指定思考等级
//	go run ./scripts/nvidia_tool_call_probe -token nvapi-XXXX                  # 覆盖 token
//	go run ./scripts/nvidia_tool_call_probe -prompt "自定义强制工具调用 prompt"
//
// Token 读取优先级:
//  1. -token 命令行 flag
//  2. 环境变量 NVIDIA_TOKEN
//  3. 文件 scripts/.secret_token
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
	"sync"
	"time"
)

// ===== 常量 =====

const (
	defaultEndpoint = "https://integrate.api.nvidia.com/v1/chat/completions"
	// 默认在架且支持深度思考与工具调用的 NIM 模型
	defaultModelKimi = "moonshotai/kimi-k3"
	// 默认深度长思考 Prompt: 逼出模型数千 Token 的极致慢思考与形式化多轮推导
	defaultPrompt = `请对以下高并发无锁初始化与内存可见性代码进行最深度的慢思考形式化推导（Deep & Extended Thinking）。
你必须在思考过程中进行多轮严密而穷尽的推演，包括但不限于：
1. 指令重排（Instruction Reordering）与多核 CPU 写缓冲区失效时序推演；
2. MESI 缓存一致性协议与 Store Buffer 异步刷回对其它核心的可见性延迟；
3. 枚举至少 4 种极端竞态条件下的异常执行路径（如部分初始化对象的引用逸出、空指针或野指针解引用）；
4. 进行自我反思并主动构造反例推翻初步结论，直到证明过程完全无瑕疵。

待分析代码：
type Instance struct { data map[string]int }
var instance *Instance
var initialized uint32

func GetInstance() *Instance {
    if atomic.LoadUint32(&initialized) == 0 {
        inst := &Instance{data: make(map[string]int)}
        inst.data["ready"] = 1
        instance = inst
        atomic.StoreUint32(&initialized, 1)
    }
    return instance
}

【强制约束】：必须进行极其充分详尽的推理（展开万字级别深思熟虑）。思考完成后，必须调用工具 report_concurrency_audit 提交结构化审查报告，绝对禁止在正文中直接输出结论。`

	// 等待上游响应的总超时。超长 thinking 可能 3-5 分钟,给足 10 分钟避免误判断流。
	totalTimeout = 10 * time.Minute
	// 给足 16384 token 保证超长思考链与复杂工具参数不被截断
	defaultMaxTokens = 16384
)

// ===== 请求协议(OpenAI Chat) =====

type toolDef struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model              string                 `json:"model"`
	Messages           []chatMessage          `json:"messages"`
	Tools              []toolDef              `json:"tools,omitempty"`
	ToolChoice         interface{}            `json:"tool_choice,omitempty"`
	MaxTokens          int                    `json:"max_tokens"`
	Temperature        float64                `json:"temperature"`
	TopP               float64                `json:"top_p"`
	Seed               int                    `json:"seed"`
	Stream             bool                   `json:"stream"`
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
}

// ===== 上游 SSE 解码 =====

type sseDelta struct {
	Role             string        `json:"role,omitempty"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	Reasoning        string        `json:"reasoning,omitempty"`
	Thinking         string        `json:"thinking,omitempty"`
	ToolCalls        []sseToolCall `json:"tool_calls,omitempty"`
}

type sseToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type sseChoice struct {
	Index        int      `json:"index"`
	Delta        sseDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type sseChunk struct {
	ID      string      `json:"id,omitempty"`
	Model   string      `json:"model,omitempty"`
	Choices []sseChoice `json:"choices,omitempty"`
	Usage   *struct {
		PromptTokens     int `json:"prompt_tokens,omitempty"`
		CompletionTokens int `json:"completion_tokens,omitempty"`
		TotalTokens      int `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

// ===== 时序事件与结果模型 =====

type frameKind string

const (
	frameReasoning     frameKind = "reasoning"
	frameContent       frameKind = "content"
	frameToolCallStart frameKind = "tool_start"
	frameToolCallArgs  frameKind = "tool_args"
	frameFinish        frameKind = "finish"
	frameDone          frameKind = "done"
	frameError         frameKind = "error"
)

type frameEvent struct {
	At     time.Duration
	Kind   frameKind
	Size   int
	Detail string
}

type modelResult struct {
	Model      string
	HTTPStatus int
	Err        error
	Wall       time.Duration

	FirstByteAt      time.Duration
	FirstReasoningAt time.Duration
	FirstContentAt   time.Duration
	ToolStartAt      time.Duration
	ToolArgsDoneAt   time.Duration
	FinishAt         time.Duration
	DoneAt           time.Duration

	ReasoningToToolGap  time.Duration
	ToolArgsDuration    time.Duration
	MaxGapBetweenFrames time.Duration
	MaxGapStartAt       time.Duration

	Events         []frameEvent
	ToolArgsLen    int
	FinishReason   string
	ReasoningBytes int
	ContentBytes   int

	ThinkingText strings.Builder
	ContentText  strings.Builder
	ToolArgsMap  map[int]*strings.Builder
	ToolNames    map[int]string
}

func newResult(model string) *modelResult {
	return &modelResult{
		Model:       model,
		Events:      make([]frameEvent, 0, 64),
		ToolArgsMap: make(map[int]*strings.Builder),
		ToolNames:   make(map[int]string),
	}
}

func (r *modelResult) record(kind frameKind, size int, detail string, now time.Duration) {
	r.Events = append(r.Events, frameEvent{At: now, Kind: kind, Size: size, Detail: detail})
	switch kind {
	case frameReasoning:
		if r.FirstReasoningAt == 0 {
			r.FirstReasoningAt = now
		}
		r.ReasoningBytes += size
	case frameContent:
		if r.FirstContentAt == 0 {
			r.FirstContentAt = now
		}
		r.ContentBytes += size
	case frameToolCallStart:
		if r.ToolStartAt == 0 {
			r.ToolStartAt = now
		}
	case frameToolCallArgs:
		r.ToolArgsLen += size
	case frameFinish:
		if r.FinishAt == 0 {
			r.FinishAt = now
		}
		r.ToolArgsDoneAt = now
	case frameDone:
		r.DoneAt = now
	}
}

func (r *modelResult) finalize() {
	if len(r.Events) == 0 {
		return
	}
	if r.ToolStartAt > 0 && r.FirstReasoningAt > 0 {
		r.ReasoningToToolGap = r.ToolStartAt - r.FirstReasoningAt
	}
	if r.ToolArgsDoneAt > 0 && r.ToolStartAt > 0 {
		r.ToolArgsDuration = r.ToolArgsDoneAt - r.ToolStartAt
	}
	var prev time.Duration
	for i, ev := range r.Events {
		if i == 0 {
			prev = ev.At
			continue
		}
		gap := ev.At - prev
		if gap > r.MaxGapBetweenFrames {
			r.MaxGapBetweenFrames = gap
			r.MaxGapStartAt = prev
		}
		prev = ev.At
	}
}

// ===== 执行一次探测 =====

func runOne(ctx0 time.Time, client *http.Client, endpoint, token, model, prompt, effort string, maxTokens int, verbose bool) *modelResult {
	res := newResult(model)

	topP := 1.0
	if strings.HasPrefix(model, "moonshotai/kimi-") {
		topP = 0.95
	}

	var kwargs map[string]interface{}
	if effort != "" && strings.ToLower(effort) != "off" && strings.ToLower(effort) != "none" {
		kwargs = map[string]interface{}{
			"thinking":         true,
			"reasoning_effort": effort,
		}
	}

	req := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Tools: []toolDef{
			{
				Type: "function",
				Function: toolFunction{
					Name:        "report_concurrency_audit",
					Description: "提交并发安全、指令重排与内存一致性深度审查报告",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"issue_category": map[string]interface{}{
								"type":        "string",
								"description": "核心缺陷类型 (如 InstructionReordering, RaceCondition, IncompleteInitialization)",
							},
							"formal_proof": map[string]interface{}{
								"type":        "string",
								"description": "形式化推导与时序竞争推演步骤",
							},
							"worst_case_scenario": map[string]interface{}{
								"type":        "string",
								"description": "最坏情况下的系统行为分析",
							},
							"recommended_fix": map[string]interface{}{
								"type":        "string",
								"description": "严格符合内存模型规范的完整修复代码",
							},
						},
						"required": []string{"issue_category", "formal_proof", "recommended_fix"},
					},
				},
			},
			{
				Type: "function",
				Function: toolFunction{
					Name:        "get_current_weather",
					Description: "查询指定城市当前的天气 (兼容简易测试)",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"city": map[string]interface{}{
								"type":        "string",
								"description": "城市名,如 北京",
							},
						},
						"required": []string{"city"},
					},
				},
			},
		},
		ToolChoice:         "required",
		MaxTokens:          maxTokens,
		Temperature:        1,
		TopP:               topP,
		Seed:               0,
		Stream:             true,
		ChatTemplateKwargs: kwargs,
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		res.Err = fmt.Errorf("marshal request: %w", err)
		return res
	}

	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		res.Err = fmt.Errorf("build request: %w", err)
		return res
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "text/event-stream")

	if verbose {
		fmt.Printf("--- [%s] 发起请求, 模式: stream=true, max_tokens=%d, effort=%q ---\n", model, maxTokens, effort)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		res.Err = fmt.Errorf("http do: %w", err)
		res.finalize()
		return res
	}
	defer resp.Body.Close()
	res.HTTPStatus = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		res.Err = fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
		res.finalize()
		return res
	}

	flusher := bufio.NewReader(resp.Body)
	scanner := bufio.NewScanner(flusher)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)

	firstByteSeen := false
	toolOpened := map[int]bool{}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		now := time.Since(ctx0)
		if !firstByteSeen {
			firstByteSeen = true
			res.FirstByteAt = now
		}
		if payload == "[DONE]" {
			res.record(frameDone, len(payload), "[DONE]", now)
			if verbose {
				fmt.Printf(" [🏁结束|+%6.2fs] SSE [DONE]\n", now.Seconds())
			}
			break
		}

		var chunk sseChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			res.record(frameKind("usage"), len(payload), fmt.Sprintf("in=%d out=%d", chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens), now)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]

		reasoningPiece := ch.Delta.ReasoningContent
		if reasoningPiece == "" {
			reasoningPiece = ch.Delta.Reasoning
		}
		if reasoningPiece == "" {
			reasoningPiece = ch.Delta.Thinking
		}

		if reasoningPiece != "" {
			res.record(frameReasoning, len(reasoningPiece), truncate(reasoningPiece, 30), now)
			res.ThinkingText.WriteString(reasoningPiece)
			if verbose {
				fmt.Printf(" [🧠思考|+%6.2fs] %s\n", now.Seconds(), escapeNewlines(reasoningPiece))
			}
		}

		if ch.Delta.Content != "" {
			res.record(frameContent, len(ch.Delta.Content), truncate(ch.Delta.Content, 30), now)
			res.ContentText.WriteString(ch.Delta.Content)
			if verbose {
				fmt.Printf(" [💬正文|+%6.2fs] %s\n", now.Seconds(), escapeNewlines(ch.Delta.Content))
			}
		}

		for _, tc := range ch.Delta.ToolCalls {
			if !toolOpened[tc.Index] {
				toolOpened[tc.Index] = true
				res.ToolNames[tc.Index] = tc.Function.Name
				res.ToolArgsMap[tc.Index] = &strings.Builder{}
				detail := fmt.Sprintf("index=%d id=%s name=%s", tc.Index, safePrefix(tc.ID, 8), tc.Function.Name)
				res.record(frameToolCallStart, len(payload), detail, now)
				if verbose {
					fmt.Printf(" [🛠️工具|+%6.2fs] start tool_call[#%d] name=%s id=%s\n", now.Seconds(), tc.Index, tc.Function.Name, tc.ID)
				}
			}
			if tc.Function.Arguments != "" {
				if b, ok := res.ToolArgsMap[tc.Index]; ok {
					b.WriteString(tc.Function.Arguments)
				}
				res.record(frameToolCallArgs, len(tc.Function.Arguments), fmt.Sprintf("index=%d args+=%d", tc.Index, len(tc.Function.Arguments)), now)
				if verbose {
					fmt.Printf(" [🛠️参数|+%6.2fs] tool_call[#%d] args_delta: %s\n", now.Seconds(), tc.Index, escapeNewlines(tc.Function.Arguments))
				}
			}
		}

		if ch.FinishReason != nil && *ch.FinishReason != "" {
			res.FinishReason = *ch.FinishReason
			res.record(frameFinish, len(payload), "finish_reason="+*ch.FinishReason, now)
			if verbose {
				fmt.Printf(" [🎯完成|+%6.2fs] finish_reason=%s\n", now.Seconds(), *ch.FinishReason)
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		res.record(frameError, 0, "scan: "+err.Error(), time.Since(ctx0))
		res.Err = err
	}
	res.Wall = time.Since(ctx0)
	res.finalize()
	return res
}

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n ")
	return s
}

// ===== 汇总与展示 =====

func fmtSeconds(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%8.2fs", d.Seconds())
}

func printTimeline(w io.Writer, r *modelResult) {
	fmt.Fprintf(w, "==== %s 帧时间线(共 %d 帧, wall=%s) ====\n", r.Model, len(r.Events), r.Wall)
	var prev time.Duration
	for i, ev := range r.Events {
		gap := time.Duration(0)
		if i > 0 {
			gap = ev.At - prev
		}
		marker := ""
		if gap == r.MaxGapBetweenFrames && r.MaxGapBetweenFrames > 500*time.Millisecond {
			marker = " ⚠️[MAX GAP]"
		}
		fmt.Fprintf(w, "  +%8.3fs (+%6.3fs)  %-12s  size=%5d  %s%s\n",
			ev.At.Seconds(), gap.Seconds(), ev.Kind, ev.Size, ev.Detail, marker)
		prev = ev.At
	}
	fmt.Fprintln(w)
}

func printDetailSection(w io.Writer, r *modelResult) {
	fmt.Fprintf(w, "---- [%s] 详细解析结果 ----\n", r.Model)
	thought := r.ThinkingText.String()
	if thought != "" {
		fmt.Fprintf(w, "【完整思考过程】(共 %d 字节):\n%s\n\n", len(thought), truncate(thought, 1200))
	} else {
		fmt.Fprintf(w, "【完整思考过程】: (无独立思考链)\n\n")
	}

	if len(r.ToolArgsMap) > 0 {
		fmt.Fprintf(w, "【工具调用参数 JSON】:\n")
		for idx, argBuf := range r.ToolArgsMap {
			name := r.ToolNames[idx]
			fmt.Fprintf(w, "  - [#%d %s]: %s\n", idx, name, argBuf.String())
		}
		fmt.Fprintln(w)
	}

	content := r.ContentText.String()
	if content != "" {
		fmt.Fprintf(w, "【正文内容】: %s\n\n", truncate(content, 600))
	}
}

func printSummary(w io.Writer, results []*modelResult) {
	fmt.Fprintln(w, "==== 关键指标对比 ====")
	header := fmt.Sprintf("%-32s", "指标")
	for _, r := range results {
		header += fmt.Sprintf(" | %22s", r.Model)
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("-", len(header)))

	row := func(name string, fn func(*modelResult) string) {
		line := fmt.Sprintf("%-32s", name)
		for _, r := range results {
			line += fmt.Sprintf(" | %22s", fn(r))
		}
		fmt.Fprintln(w, line)
	}

	row("HTTP 状态", func(r *modelResult) string { return fmt.Sprintf("%d", r.HTTPStatus) })
	row("finish_reason", func(r *modelResult) string {
		if r.FinishReason == "" {
			return "-"
		}
		return r.FinishReason
	})
	row("错误", func(r *modelResult) string {
		if r.Err == nil {
			return "-"
		}
		return truncate(r.Err.Error(), 22)
	})
	row("TTFB(首帧)", func(r *modelResult) string { return fmtSeconds(r.FirstByteAt) })
	row("reasoning 首帧", func(r *modelResult) string { return fmtSeconds(r.FirstReasoningAt) })
	row("content 首帧", func(r *modelResult) string { return fmtSeconds(r.FirstContentAt) })
	row("★ tool_calls 首帧", func(r *modelResult) string { return fmtSeconds(r.ToolStartAt) })
	row("tool 参数完整就绪", func(r *modelResult) string { return fmtSeconds(r.ToolArgsDoneAt) })
	row("[DONE] 到达", func(r *modelResult) string { return fmtSeconds(r.DoneAt) })
	row("reasoning 总字节", func(r *modelResult) string { return fmt.Sprintf("%d", r.ReasoningBytes) })
	row("content 总字节", func(r *modelResult) string { return fmt.Sprintf("%d", r.ContentBytes) })
	row("tool arguments 总字节", func(r *modelResult) string { return fmt.Sprintf("%d", r.ToolArgsLen) })
	row("★ reasoning→tool 间隔", func(r *modelResult) string { return fmtSeconds(r.ReasoningToToolGap) })
	row("★ tool 段时长(蓄流窗口)", func(r *modelResult) string { return fmtSeconds(r.ToolArgsDuration) })
	row("★★ 客户端最大无字节窗口", func(r *modelResult) string { return fmtSeconds(r.MaxGapBetweenFrames) })
	row("   最大窗口开始于", func(r *modelResult) string { return fmtSeconds(r.MaxGapStartAt) })
	fmt.Fprintln(w)
}

func printVerdict(w io.Writer, results []*modelResult) {
	if len(results) < 2 {
		return
	}
	okResults := make([]*modelResult, 0, len(results))
	for _, r := range results {
		if r.Err == nil && r.HTTPStatus == 200 {
			okResults = append(okResults, r)
		}
	}
	if len(okResults) != len(results) {
		fmt.Fprintln(w, "==== 结论:存在失败请求,无法对比 ====")
		return
	}

	sort.Slice(okResults, func(i, j int) bool {
		return okResults[i].MaxGapBetweenFrames > okResults[j].MaxGapBetweenFrames
	})
	worst := okResults[0]
	best := okResults[len(okResults)-1]

	fmt.Fprintln(w, "==== 结论 ====")
	fmt.Fprintf(w, "客户端「最大无字节窗口」最大者: %s = %s\n", worst.Model, worst.MaxGapBetweenFrames)
	fmt.Fprintf(w, "客户端「最大无字节窗口」最小者: %s = %s\n", best.Model, best.MaxGapBetweenFrames)
	if worst.MaxGapBetweenFrames > 0 {
		ratio := float64(worst.MaxGapBetweenFrames) / float64(best.MaxGapBetweenFrames+1)
		fmt.Fprintf(w, "差距倍数: %.1fx\n", ratio)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "■ 提示: 代理在 tool_use 出现后会蓄流直到 finish, 此期间客户端静默。")
	fmt.Fprintf(w, "  %s 的 tool 段时长 = %s\n", worst.Model, worst.ToolArgsDuration)
	fmt.Fprintf(w, "  %s 的 tool 段时长 = %s\n", best.Model, best.ToolArgsDuration)
}

// ===== Token 解析 =====

func resolveToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	if env := os.Getenv("NVIDIA_TOKEN"); env != "" {
		return env
	}
	secretFile := filepath.Join(".", "scripts", ".secret_token")
	if data, err := os.ReadFile(secretFile); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func sanitizeFileName(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(s)
}

// ===== 主入口 =====

func main() {
	var (
		modelsFlag = flag.String("models", defaultModelKimi, "逗号分隔的模型列表")
		tokenFlag  = flag.String("token", "", "NVIDIA API token(留空则按 env NVIDIA_TOKEN / scripts/.secret_token 兜底)")
		promptFlag = flag.String("prompt", defaultPrompt, "强制触发工具调用的用户 prompt")
		effortFlag = flag.String("effort", "max", "思考等级(如 high / max, 留空表示不强设)")
		maxTokens  = flag.Int("max-tokens", defaultMaxTokens, "上游 max_tokens")
		endpoint   = flag.String("endpoint", defaultEndpoint, "NVIDIA chat completions 端点")
		logDir     = flag.String("logdir", "logs", "时序日志输出目录")
		verbose    = flag.Bool("verbose", true, "是否实时流式打字输出日志 (看到思考链与工具调用)")
		parallel   = flag.Bool("parallel", false, "是否并发多模型 (默认串行)")
	)
	flag.Parse()

	token := resolveToken(*tokenFlag)
	if token == "" {
		fmt.Fprintln(os.Stderr, "[致命] 未找到 NVIDIA token: 请用 -token 或环境变量 NVIDIA_TOKEN 或 scripts/.secret_token 提供")
		os.Exit(2)
	}

	models := []string{}
	for _, m := range strings.Split(*modelsFlag, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			models = append(models, m)
		}
	}
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "[致命] models 为空")
		os.Exit(2)
	}

	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[警告] 无法创建日志目录 %s: %v\n", *logDir, err)
	}

	client := &http.Client{
		Timeout: totalTimeout,
		Transport: &http.Transport{
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: true,
		},
	}

	ts := time.Now().Format("20060102_150405")
	fmt.Printf("==== nvidia_tool_call_probe ====\n")
	fmt.Printf("时间: %s | 端点: %s\n", ts, *endpoint)
	fmt.Printf("模型: %v\n", models)
	fmt.Printf("Effort: %q\n", *effortFlag)
	fmt.Printf("Prompt: %s\n", truncate(*promptFlag, 60))
	fmt.Printf("Token: %s...\n", safePrefix(token, 12))
	fmt.Printf("实时输出: %v\n\n", *verbose)

	results := make([]*modelResult, 0, len(models))
	var mu sync.Mutex
	var wg sync.WaitGroup

	run := func(model string) {
		defer wg.Done()
		start := time.Now()
		r := runOne(start, client, *endpoint, token, model, *promptFlag, *effortFlag, *maxTokens, *verbose)
		mu.Lock()
		results = append(results, r)
		mu.Unlock()

		logFile := filepath.Join(*logDir, fmt.Sprintf("tool_probe_%s_%s.log", ts, sanitizeFileName(model)))
		f, err := os.Create(logFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[警告] 写日志 %s 失败: %v\n", logFile, err)
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "model=%s endpoint=%s prompt=%q effort=%q\n\n", r.Model, *endpoint, *promptFlag, *effortFlag)
		printTimeline(f, r)
		printDetailSection(f, r)
		printSummary(f, []*modelResult{r})
		fmt.Fprintf(f, "[完成] wall=%s status=%d err=%v\n", r.Wall, r.HTTPStatus, r.Err)
		fmt.Printf("\n[日志] %s → %s\n", model, logFile)
	}

	if *parallel {
		for _, m := range models {
			wg.Add(1)
			go run(m)
		}
	} else {
		for _, m := range models {
			wg.Add(1)
			run(m)
		}
	}
	wg.Wait()

	ordered := make([]*modelResult, 0, len(models))
	for _, m := range models {
		for _, r := range results {
			if r.Model == m {
				ordered = append(ordered, r)
				break
			}
		}
	}

	fmt.Println()
	for _, r := range ordered {
		printDetailSection(os.Stdout, r)
	}
	printSummary(os.Stdout, ordered)
	printVerdict(os.Stdout, ordered)

	summaryFile := filepath.Join(*logDir, fmt.Sprintf("tool_probe_%s_summary.log", ts))
	if f, err := os.Create(summaryFile); err == nil {
		defer f.Close()
		fmt.Fprintf(f, "model 顺序: %v\n\n", models)
		for _, r := range ordered {
			printDetailSection(f, r)
		}
		printSummary(f, ordered)
		printVerdict(f, ordered)
		fmt.Printf("\n[汇总日志] %s\n", summaryFile)
	}
}
