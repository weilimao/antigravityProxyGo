// Command nvidia_thinking_level_probe 直连 NVIDIA 上游 NIM,对 deepseek-ai/deepseek-v4-flash
// 分别注入 chat_template_kwargs:{thinking:true, reasoning_effort:"high"|"max"},逐帧打印 SSE,
// 专项核对:上游带上思考等级后,响应里到底带不带思考内容(reasoning_content / reasoning 字段)。
//
// 与已有的 scripts/nvidia_upstream_stream_probe 的区别:
//   - 那个探"流式真伪 + gzip 攒批指纹";
//   - 这个探"思考等级注入 → 输出是否带思考内容",请求体带 chat_template_kwargs,两档轮询。
//
// 不经过中继入口、不写统计文件,只打真实上游两次推理调用(high / max 各一),日志落盘。
//
// 用法:
//
//	go run ./scripts/nvidia_thinking_level_probe                      # high + max 两档轮询
//	go run ./scripts/nvidia_thinking_level_probe -model deepseek-ai/deepseek-v4-flash
//	go run ./scripts/nvidia_thinking_level_probe -efforts high         # 只测一档
//	go run ./scripts/nvidia_thinking_level_probe -prompt "详述你的推理过程"
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

// ===== 以下结构体复刻 internal/relay/nvidia_translate.go 的上游请求/响应字段子集 =====
// 不 import internal/relay(避免拖中继依赖),只复制直连探测用到的子集字段。

// ChatStreamOptions 对应 nvidia_translate.go:597 的 ChatStreamOptions。
// stream=true 时项目会强制注入 include_usage=true(见 nvidia.go:373-376),这里照搬。
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatMessage 对应 nvidia_translate.go:557 的 ChatMessage,仅保留直连探测用到的字段。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChatRequest 对应 nvidia_translate.go:602 的 OpenAIChatRequest 子集,
// 关键新增 ChatTemplateKwargs 字段(与 nvidia_translate.go:1270 同名同 tag),
// 用于把思考开关与等级透传给 NIM 上游。
type OpenAIChatRequest struct {
	Model             string             `json:"model"`
	Messages          []ChatMessage     `json:"messages"`
	Stream            bool              `json:"stream,omitempty"`
	StreamOptions     *ChatStreamOptions `json:"stream_options,omitempty"`
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
}

// 默认参数(可用 flag 覆盖)。
const (
	defaultBaseURL = "https://integrate.api.nvidia.com/v1"
	// 思考链探针:默认用 deepseek-ai/deepseek-v4-flash(NIM 官方推理模型,支持 thinking + reasoning_effort)。
	defaultModel = "deepseek-ai/deepseek-v4-flash"
	// 给真实代码片段让它分析,逼出思考链(与 nvidia_upstream_stream_probe 同一 prompt)。
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
	// 硬编码 Token(用户指定)。仅本地手测用,勿提交到公开仓库。
	// 与 scripts/nvidia_upstream_stream_probe/main.go:75 同一 token,复用不新增。
	hardcodedToken = "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"
)

// efforts 两档轮询的默认取值。deepseek-v4-flash 官方仅认 high / max 两档。
var defaultEfforts = []string{"high", "max"}

// effortStat 记录一档思考等级探测的统计结果,供跨档汇总对照。
type effortStat struct {
	effort          string
	status          int
	err             string
	reasoningFrames int // reasoning_content 命中帧数
	reasoningAltFrm int // reasoning 命中帧数(NIM 另一种承载形态)
	thinkingFrm     int // thinking 命中帧数(若有)
	contentFrames   int // content 正文帧数
	hasThinking     bool
	logPath         string
}

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "NVIDIA 上游 BaseURL(默认 https://integrate.api.nvidia.com/v1)")
	token := flag.String("token", hardcodedToken, "NVIDIA AccessToken(默认硬编码值)")
	model := flag.String("model", defaultModel, "上游模型 ID")
	prompt := flag.String("prompt", defaultPrompt, "测试 prompt")
	// efforts:逗号分隔的思考等级列表,默认 "high,max"。可只填一档。
	effortsStr := flag.String("efforts", "high,max", "要测的思考等级(逗号分隔,如 high 或 high,max)")
	// 日志文件目录:默认写当前工作目录 logs/ 子目录,每档一个文件。
	logDir := flag.String("log-dir", "", "日志文件输出目录(不填则写到 ./logs/)")
	flag.Parse()

	efforts := parseEfforts(*effortsStr)
	if len(efforts) == 0 {
		fmt.Fprintf(os.Stderr, "[错误] -efforts 解析为空,请填如 high 或 high,max\n")
		os.Exit(1)
	}

	// ===== tee: 每档一个独立日志文件,屏幕也实时输出 =====
	// 总览日志:记录跨档汇总(屏幕 + 总览文件)。
	summaryPath := buildLogPath(*logDir, "", "summary")
	realStdout := dupStdout()
	startTee(realStdout, summaryPath)

	// 上游 URL: 与 nvidia.go:380-384 一致的拼法。
	// {BaseURL}/v1/chat/completions; 若 BaseURL 已以 /v1 结尾则用 {BaseURL}/chat/completions。
	trimmed := strings.TrimRight(*baseURL, "/")
	targetURL := trimmed + "/v1/chat/completions"
	if strings.HasSuffix(trimmed, "/v1") {
		targetURL = trimmed + "/chat/completions"
	}

	fmt.Printf("==== NVIDIA NIM 思考等级探针 ====\n")
	fmt.Printf("BaseURL  : %s\n", *baseURL)
	fmt.Printf("Target   : %s\n", targetURL)
	fmt.Printf("Model    : %s\n", *model)
	fmt.Printf("Efforts  : %v\n", efforts)
	fmt.Printf("Prompt   : %q\n", *prompt)
	fmt.Printf("Token    : %s...\n", safePrefix(*token, 14))
	fmt.Printf("总览日志 : %s (跨档汇总写这里)\n", summaryPath)
	fmt.Printf("分档日志 : 每档单独一个文件,见下方各档输出\n\n")

	// 跨档统计:每档的思考字段命中帧数,便于末尾汇总对照。
	var stats []*effortStat

	for _, effort := range efforts {
		fmt.Printf("######## 思考等级 running_effort=%s ########\n", effort)

		// 每档切独立日志文件(该档 tee 在 runOnceEffort 内部 start/flush)。
		effortLog := buildLogPath(*logDir, "", "effort_"+effort)
		st := &effortStat{effort: effort, status: -1, logPath: effortLog}
		stats = append(stats, st)

		runOnceEffort(targetURL, *token, *model, *prompt, effort, effortLog, st)
		fmt.Println()
	}

	// ===== 跨档汇总 =====
	fmt.Println("==================== 思考内容跨档汇总 ====================")
	fmt.Printf("%-6s %-7s %-12s %-12s %-10s %-9s\n",
		"Effort", "Status", "reasoning_c", "reasoning", "thinking", "content")
	anyHasThink := false
	for _, st := range stats {
		mark := "✗ 不带思考"
		if st.reasoningFrames > 0 || st.reasoningAltFrm > 0 || st.thinkingFrm > 0 {
			mark = "✓ 带思考内容"
			anyHasThink = true
		}
		if st.err != "" {
			mark = "✗ 报错:" + truncate(st.err, 30)
		}
		fmt.Printf("%-6s %-7d %-12d %-12d %-10d %-9d  %s\n",
			st.effort, st.status, st.reasoningFrames, st.reasoningAltFrm, st.thinkingFrm, st.contentFrames, mark)
		fmt.Printf("       日志: %s\n", st.logPath)
	}
	fmt.Println("==========================================================")
	if anyHasThink {
		fmt.Println("最终结论: 上游在至少一档下用独立思考字段(reasoning_content/reasoning/thinking)承载推理过程 → 思考内容随 chat_template_kwargs 回来了 ✓")
	} else {
		fmt.Println("最终结论: 各档 delta 均未见独立思考字段 → 上游可能没按 chat_template_kwargs 开思考(模型名/参数/档不被认),请查各档日志的响应头与错误体")
	}

	// 退出前 flush 总览日志: 关 pipe 写端让 tee goroutine 读到 EOF 并刷盘。
	flushTee()
}

// parseEfforts 解析 -efforts 逗号分隔字符串,去空白去重,保留出现顺序。
func parseEfforts(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// runOnceEffort 对一档思考等级发起一次流式上游请求(带 chat_template_kwargs),
// 逐帧打印 SSE 并统计思考字段命中。effortLog 为该档独立日志文件路径。
// stat 用于回填该档统计(供跨档汇总)。
func runOnceEffort(targetURL, token, model, prompt, effort, effortLog string, stat *effortStat) {
	// 该档用独立内存 buf 收集全部输出(避免跨档共享 pipe 切换导致的屏幕串档竞态):
	// 本档所有 fmt.Print* 先写进 buf,该档结束后再一次性冲刷到「真实屏幕 + 该档日志文件」。
	startEffortBuf()

	// 构造请求体: stream=true 强制注入 include_usage(与 nvidia.go:373-376 一致)。
	chatReq := &OpenAIChatRequest{
		Model:    model,
		Messages: []ChatMessage{{Role: "user", Content: prompt}},
		Stream:   true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		// 关键: 注入思考开关与等级(NIM 官方 deepseek-v4-flash 形态)。
		ChatTemplateKwargs: map[string]interface{}{
			"thinking":         true,
			"reasoning_effort": effort,
		},
	}
	body, err := json.Marshal(chatReq)
	if err != nil {
		stat.err = "构造请求体失败: " + err.Error()
		fmt.Printf("[构造请求体失败] %v\n", err)
		flushEffortBuf(effortLog)
		return
	}
	fmt.Printf("[请求体] chat_template_kwargs={thinking:true, reasoning_effort:%q}\n", effort)
	fmt.Printf("[请求体] 完整 JSON: %s\n\n", string(body))

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		stat.err = "构造请求失败: " + err.Error()
		fmt.Printf("[构造请求失败] %v\n", err)
		flushEffortBuf(effortLog)
		return
	}
	// 请求头与 nvidia.go:397-399 一致: 不注入 anthropic 头。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	// 流式用无读超时,避免长输出被提前掐断。
	client := &http.Client{Timeout: 0}

	resp, err := client.Do(req)
	if err != nil {
		stat.err = "上游请求失败: " + err.Error()
		fmt.Printf("[上游请求失败] 已耗时 %v | %v\n", time.Since(start), err)
		flushEffortBuf(effortLog)
		return
	}
	defer resp.Body.Close()

	stat.status = resp.StatusCode
	fmt.Printf("[响应状态码] %d | 发起后 %v\n", resp.StatusCode, time.Since(start))
	fmt.Printf("[响应头 Content-Type] %q\n", resp.Header.Get("Content-Type"))
	fmt.Printf("[响应头 Transfer-Encoding] %q\n", resp.Header.Get("Transfer-Encoding"))

	if resp.StatusCode != http.StatusOK {
		// 上游非 200: 一次性读错误体打印,这是判"模型名/参数不被认"的关键证据。
		errBytes, _ := io.ReadAll(resp.Body)
		errStr := truncate(string(errBytes), 2000)
		stat.err = fmt.Sprintf("上游 %d: %s", resp.StatusCode, truncate(string(errBytes), 80))
		fmt.Printf("[上游非 200 错误体,effort=%s]\n%s\n", effort, errStr)
		flushEffortBuf(effortLog)
		return
	}

	// 流式逐行扫描: 与 nvidia.go:613-650 一致的 8MB 单行缓冲,避免长帧被截断丢 usage。
	fmt.Printf("[流式] effort=%s 逐行读取 SSE, 带前缀 [行号 | ms | cumB]:\n", effort)
	flusher := bufio.NewReader(resp.Body)
	scanner := bufio.NewScanner(flusher)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	totalBytes := 0
	firstFrameAt := time.Duration(0)
	firstFrameSet := false
	chunkCount := 0
	dataCount := 0
	// 思考字段命中累计: "content" / "reasoning_content" / "reasoning" / "thinking" 各出现多少帧。
	deltaFieldHits := map[string]int{}
	var lastUsage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	// 思考内容片段累积,便于末尾打印推理走向是否真的有内容(而非空串握手帧)。
	var thinkingTextBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !firstFrameSet {
			firstFrameAt = time.Since(start)
			firstFrameSet = true
		}
		lineNo++
		totalBytes += len(line) + 1
		fmt.Printf("  [#%-4d | %6dms | cum=%dB] %s\n", lineNo, time.Since(start).Milliseconds(), totalBytes, line)

		if line == "" {
			continue // SSE 事件分隔
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			fmt.Printf("  ↑ 收到 [DONE],effort=%s 流结束\n", effort)
			break
		}
		dataCount++
		// 嗅探 usage / finish_reason / delta 字段。
		var chunk struct {
			Choices []struct {
				Index        int             `json:"index"`
				Delta        json.RawMessage `json:"delta"`
				FinishReason string          `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil {
			chunkCount++
			if chunk.Usage != nil {
				lastUsage = chunk.Usage
				fmt.Printf("  ↑ 本帧命中 usage: %+v\n", *chunk.Usage)
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
				fmt.Printf("  ↑ 本帧 finish_reason=%s\n", chunk.Choices[0].FinishReason)
			}

			// ===== 思考链探针核心:穷举 delta 字段,不预设字段名 =====
			// 把 delta 原样解析成 map,列出所有 key,并高亮 content / reasoning_content /
			// reasoning / thinking 这几类,让上游自己暴露它用什么标记承载思考。
			if len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta) > 0 && string(chunk.Choices[0].Delta) != "{}" {
				var deltaMap map[string]json.RawMessage
				if json.Unmarshal(chunk.Choices[0].Delta, &deltaMap) == nil {
					keys := make([]string, 0, len(deltaMap))
					for k := range deltaMap {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					var tags []string
					thinkers := map[string]bool{
						"reasoning_content": true,
						"reasoning":         true,
						"thinking":          true,
						"reasoning_effort":  true,
					}
					for _, k := range keys {
						deltaFieldHits[k]++
						if thinkers[k] {
							tags = append(tags, k)
						}
					}
					prefix := "  ✓ delta 字段: [" + strings.Join(keys, ", ") + "]"
					if len(tags) > 0 {
						prefix += "  <<< 思考标记命中: " + strings.Join(tags, ", ") + " >>>"
					}
					fmt.Println(prefix)
					// 逐字段打印取值,思考/正文字段全打,其它截断。
					for _, k := range keys {
						raw := string(deltaMap[k])
						if thinkers[k] || k == "content" {
							fmt.Printf("      · %s = %s\n", k, raw)
							// 累积思考文本(reasoning_content / reasoning / thinking 都算)。
							if k == "reasoning_content" || k == "reasoning" || k == "thinking" {
								var s string
								if json.Unmarshal(deltaMap[k], &s) == nil {
									thinkingTextBuf.WriteString(s)
								}
							}
						} else {
							fmt.Printf("      · %s = %s\n", k, truncate(raw, 200))
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("[流式扫描出错] %v\n", err)
	}

	// 回填该档统计(供跨档汇总)。
	stat.reasoningFrames = deltaFieldHits["reasoning_content"]
	stat.reasoningAltFrm = deltaFieldHits["reasoning"]
	stat.thinkingFrm = deltaFieldHits["thinking"]
	stat.contentFrames = deltaFieldHits["content"]
	stat.hasThinking = stat.reasoningFrames > 0 || stat.reasoningAltFrm > 0 || stat.thinkingFrm > 0

	fmt.Println("---- effort=" + effort + " 流式汇总 ----")
	fmt.Printf("首帧时延      : %v\n", firstFrameAt)
	fmt.Printf("总行数        : %d\n", lineNo)
	fmt.Printf("data: 帧数    : %d (成功解析 chunk: %d)\n", dataCount, chunkCount)
	fmt.Printf("累计字节      : %d\n", totalBytes)
	fmt.Printf("总耗时        : %v\n", time.Since(start))
	if lastUsage != nil {
		fmt.Printf("末帧 usage    : %+v\n", *lastUsage)
	} else {
		fmt.Printf("末帧 usage    : (未命中)\n")
	}
	fmt.Println("---- effort=" + effort + " 思考链标记统计 ----")
	if len(deltaFieldHits) == 0 {
		fmt.Println("delta 无任何增量字段命中,上游可能没流式吐 delta 或空响应 → ✗")
	} else {
		type fieldHit struct {
			name  string
			count int
		}
		var hits []fieldHit
		for k, c := range deltaFieldHits {
			hits = append(hits, fieldHit{k, c})
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].count > hits[j].count })
		for _, h := range hits {
			mark := ""
			switch h.name {
			case "content":
				mark = "(正文)"
			case "reasoning_content", "reasoning", "thinking":
				mark = "(思考链 ✓)"
			}
			fmt.Printf("  %-20s 出现 %d 帧 %s\n", h.name, h.count, mark)
		}
		if stat.hasThinking {
			fmt.Printf("结论: effort=%s → 上游用独立思考字段承载推理,思考内容随 chat_template_kwargs 回来 ✓\n", effort)
			// 打印累积思考文本前 600 字,直观确认有真实推理内容。
			thought := thinkingTextBuf.String()
			if thought == "" {
				fmt.Printf("思考字段命中但文本为空(可能上游只发空串握手帧),需结合逐帧原文判断。\n")
			} else {
				fmt.Printf("累积思考文本(前 600 字):\n%s\n", truncate(thought, 600))
			}
		} else {
			fmt.Printf("结论: effort=%s → delta 无独立思考字段,思考内容可能混在正文或不开启 ✗\n", effort)
		}
	}

	flushEffortBuf(effortLog)
}

// truncate 截断长字符串用于打印, 复刻 nvidia.go:200-206 的 truncateBody 行为。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// safePrefix 安全打印 token 前缀,避免误把完整 key 打到日志。
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// buildLogPath 构造日志文件路径:dir 为空时落在 ./logs/,文件名 thinking_probe_<时间>_effort_<档>.log。
// 当 effortPart 为空时不拼 effort 段(用于总览 summary)。
func buildLogPath(dir, _ string, effortPart string) string {
	if dir == "" {
		dir = filepath.Join(".", "logs")
	}
	stamp := time.Now().Format("20060102_150405")
	name := "thinking_probe_" + stamp
	if effortPart != "" {
		name += "_" + effortPart
	}
	name += ".log"
	return filepath.Join(dir, name)
}

// dupStdout 保存当前真实屏幕的 os.Stdout 引用(不关闭它),供 tee 恢复使用。
func dupStdout() *os.File {
	return os.Stdout
}

// ===== tee 实现(两套:总览 + 分档,互不干扰) =====
//
// 设计要点:外层 main 起一个总览 tee(屏幕+总览文件);每档在 runOnceEffort 内部临时
// 再起一个分档 tee(屏幕+该档文件),分档 tee flush 后把 os.Stdout 恢复回总览 pipe 写端,
// 这样跨档继续写总览。两套用不同的包级写端句柄隔离。

var (
	// 总览 tee 写端(main 起,退出前 flush)。
	summaryWriter  *os.File
	summaryLogPath string
	summaryScreen  *os.File

	// 分档 buf:本档所有 fmt.Print* 先收容进 buf,该档结束后串行冲刷,无跨档串档竞态。
	effortBuf         *bytes.Buffer
	effortSavedOut    *os.File
	effortPipeWriter  *os.File // 本档 pipe 写端(=切换前的 os.Stdout),flush 时关闭触发收容 goroutine 收齐
)

// startTee 启动总览 tee:拦截 os.Stdout → goroutine 把读端字节 MultiWriter 写「真实屏幕 + 总览文件」。
// 总览全程不切换,故无串档竞态。
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

// startEffortBuf 拦截本档输出:把 os.Stdout 指向一个新 pipe 写端,goroutine 把读端字节
// 写进本档 buf(只收容,不直接写屏幕/文件)。本档结束 flushEffortBuf 时关闭写端 → goroutine 收到 EOF、buf 收齐,
// 主协程再串行把 buf 一次冲到「屏幕(总览 pipe)+ 该档文件」。无跨档 pipe 切换竞态。
func startEffortBuf() {
	buf := new(bytes.Buffer)
	r, w, err := os.Pipe()
	if err != nil {
		// pipe 失败:退化为不拦截(本档直接写总览 pipe),牺牲分档文件隔离但不出错。
		fmt.Fprintf(os.Stdout, "[warn] effort pipe 失败,本档输出混入总览: %v\n", err)
		effortBuf = nil
		effortSavedOut = nil
		return
	}
	effortBuf = buf
	effortSavedOut = os.Stdout // 保存总览 pipe 写端,flush 后恢复
	effortPipeWriter = w       // 保存本档 pipe 写端,flush 时关闭触发 goroutine 收齐
	os.Stdout = w              // 本档 fmt.Print* 进 pipe
	go func() {
		_, _ = io.Copy(buf, r)
		_ = r.Close()
	}()
}

// flushEffortBuf 关闭本档 pipe 写端,等收容 goroutine 读到 EOF 把 buf 收齐,
// 再把 buf 一次性冲到「该档日志文件 + 屏幕(经总览 pipe)」,最后恢复 os.Stdout。
// goroutine-less 串行最后一冲,无跨档串档。
func flushEffortBuf(logPath string) {
	if effortBuf == nil {
		// 退化为不拦截的档:无需冲刷,os.Stdout 始终是总览写端。
		return
	}
	// 先恢复 os.Stdout 到总览 pipe 写端,使后续 fmt 走总览。
	if effortSavedOut != nil {
		os.Stdout = effortSavedOut
	}
	effortSavedOut = nil

	// 关闭当前 pipe 写端,触发收容 goroutine 读到 EOF,完成 buf 收齐。
	// 注意 effortSavedOut 是总览写端,os.Stdout 已切回它;这里关闭的是本档 pipe 写端。
	// 本档 pipe 写端 = 切换前的 os.Stdout,需单独持有句柄。
	if effortPipeWriter != nil {
		_ = effortPipeWriter.Close()
		effortPipeWriter = nil
	}

	// 此时 buf 已收齐。bytes.Buffer.WriteTo 会推进读指针,连续两次 WriteTo 第二次为空,
	// 故先取字符串再分别写文件与屏幕。
	content := effortBuf.String()

	// 写该档日志文件。
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(os.Stdout, "[warn] 建分档日志父目录失败(%s): %v\n", logPath, err)
	}
	if f, err := os.Create(logPath); err == nil {
		_, _ = f.WriteString(content)
		_ = f.Close()
	} else {
		fmt.Fprintf(os.Stdout, "[warn] 创建分档日志失败(%s): %v\n", logPath, err)
	}
	// buf 内容也写到屏幕(经总览 pipe → 屏幕+总览文件,跨档串行无交叠)。
	_, _ = os.Stdout.WriteString(content)
	fmt.Fprintf(os.Stdout, "[log] 分档日志已写入: %s\n", logPath)
	effortBuf = nil
}

// flushTee 关闭总览 pipe 写端(在 main 退出前调用)。
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

// 保留 io 引用以防某些工具链把 io 标记为未使用。
var _ = io.EOF
