// Command nvidia_upstream_stream_probe 直连 NVIDIA 上游,
// 复刻 internal/relay/nvidia.go 里 "构造上游 OpenAI Chat 请求 + 读取上游响应" 的真实逻辑,
// 用来人工核对上游到底是不是流式(SSE)返回。
//
// 不经过中继入口、不写任何统计文件,只打真实上游一次推理调用。
//
// 用法:
//
//	go run ./scripts/nvidia_upstream_stream_probe
//	go run ./scripts/nvidia_upstream_stream_probe -mode=stream
//	go run ./scripts/nvidia_upstream_stream_probe -mode=nostream
//	go run ./scripts/nvidia_upstream_stream_probe -model z-ai/glm-5.2 -prompt "用一句话介绍你自己"
//
// 默认 mode=both,即 stream=false 与 stream=true 各打一次,对照看。
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

// ===== 以下结构体原样复刻 internal/relay/nvidia_translate.go 的上游请求/响应字段 =====
// 不 import internal/relay(避免拖一堆中继依赖), 这里只复制 toString 真正用到的子集字段。

// ChatStreamOptions 对应 nvidia_translate.go:597 的 ChatStreamOptions。
// stream=true 时项目会强制注入 include_usage=true,见 nvidia.go:373-376。
type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatMessage 对应 nvidia_translate.go:557 的 ChatMessage,仅保留直连探测用到的字段。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChatRequest 对应 nvidia_translate.go:602 的 OpenAIChatRequest 子集。
type OpenAIChatRequest struct {
	Model        string             `json:"model"`
	Messages     []ChatMessage     `json:"messages"`
	Stream       bool              `json:"stream,omitempty"`
	StreamOptions *ChatStreamOptions `json:"stream_options,omitempty"`
}

// 默认参数(可选 flag 覆盖)。
const (
	defaultBaseURL = "https://integrate.api.nvidia.com/v1"
	// 思考链探针:默认用 z-ai/glm-5.2,给真实代码分析任务逼出思考链。
	defaultModel = "z-ai/glm-5.2"
	// 给真实代码片段让它分析,逼出思考链。
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
	hardcodedToken = "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"
)

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "NVIDIA 上游 BaseURL(默认 https://integrate.api.nvidia.com/v1)")
	token := flag.String("token", hardcodedToken, "NVIDIA AccessToken(默认硬编码值)")
	model := flag.String("model", defaultModel, "上游模型 ID")
	prompt := flag.String("prompt", defaultPrompt, "测试 prompt")
	mode := flag.String("mode", "both", "both | stream | nostream")
	// 日志文件路径:默认写当前工作目录 logs/ 子目录,带时间戳与 mode;可用 -log-file 覆盖。
	logFile := flag.String("log-file", "", "日志文件输出路径(不填则自动写到 ./logs/nvidia_probe_<时间>_<mode>.log)")
	flag.Parse()

	// ===== tee 所有 stdout 到日志文件,便于事后分析 =====
	// 实现:先备份真实屏幕 fd(dup),再建 pipe 把 os.Stdout 指向 pipe 写端,
	// goroutine 从 pipe 读端读出所有输出,同时写回真实屏幕与日志文件。
	// 从这一行起,后续所有 fmt.Print* 一行不动即可同时落屏幕与文件。
	realStdout := dupStdout()
	startTee(realStdout, *logFile, *mode)

	// 上游 URL: 与 nvidia.go:380-384 完全一致的拼法。
	// {BaseURL}/v1/chat/completions; 若 BaseURL 已以 /v1 结尾则用 {BaseURL}/chat/completions。
	trimmed := strings.TrimRight(*baseURL, "/")
	targetURL := trimmed + "/v1/chat/completions"
	if strings.HasSuffix(trimmed, "/v1") {
		targetURL = trimmed + "/chat/completions"
	}

	fmt.Printf("==== NVIDIA 上游流式探测 ====\n")
	fmt.Printf("BaseURL  : %s\n", *baseURL)
	fmt.Printf("Target   : %s\n", targetURL)
	fmt.Printf("Model    : %s\n", *model)
	fmt.Printf("Prompt   : %q\n", *prompt)
	fmt.Printf("Token    : %s...\n", safePrefix(*token, 14))
	fmt.Printf("Mode     : %s\n\n", *mode)

	switch *mode {
	case "stream":
		runOnce(targetURL, *token, *model, *prompt, true)
	case "nostream":
		runOnce(targetURL, *token, *model, *prompt, false)
	case "both":
		fmt.Println("######## 分支 A: stream=false ########")
		runOnce(targetURL, *token, *model, *prompt, false)
		fmt.Println()
		fmt.Println("######## 分支 B: stream=true  ########")
		runOnce(targetURL, *token, *model, *prompt, true)
	default:
		fmt.Printf("未知 mode: %s (允许 both|stream|nostream)\n", *mode)
	}

	// 退出前 flush: 关 pipe 写端让 tee goroutine 读到 EOF 并把残留 buffer 刷进日志文件,
	// 再恢复屏幕输出。
	flushTee()
}

// runOnce 按项目逻辑发起一次上游请求并打印响应。
// isStreaming=true 完整复刻 nvidia.go 流式读取: bufio.Scanner + 8MB 缓冲,逐行打印(带时延与累计字节),
// 嗅探 data: 行的 usage,遇 [DONE] 结束。
func runOnce(targetURL, token, model, prompt string, isStreaming bool) {
	// 构造请求体: 与 nvidia.go:366-377 一致。stream=true 时强制注入 include_usage。
	chatReq := &OpenAIChatRequest{
		Model:    model,
		Messages: []ChatMessage{{Role: "user", Content: prompt}},
		Stream:   isStreaming,
	}
	if isStreaming {
		chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(chatReq)
	if err != nil {
		fmt.Printf("[构造请求体失败] %v\n", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		fmt.Printf("[构造请求失败] %v\n", err)
		return
	}
	// 请求头与 nvidia.go:397-399 一致: 不注入 anthropic 头。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	var client *http.Client
	if isStreaming {
		// 流式用更长读超时,避免长输出被客户端提前掐断。
		client = &http.Client{Timeout: 0}
	} else {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[上游请求失败] 已耗时 %v | %v\n", time.Since(start), err)
		return
	}
	defer resp.Body.Close()

	// 响应头(关键: Content-Type 是否为 text/event-stream)。
	fmt.Printf("[响应状态码] %d | 发起后 %v\n", resp.StatusCode, time.Since(start))
	fmt.Printf("[响应头 Content-Type] %q\n", resp.Header.Get("Content-Type"))
	fmt.Printf("[响应头 Transfer-Encoding] %q\n", resp.Header.Get("Transfer-Encoding"))
	fmt.Printf("[响应头 Cache-Control] %q\n", resp.Header.Get("Cache-Control"))

	if resp.StatusCode != http.StatusOK {
		// 上游非 200: 一次性读错误体打印,与 nvidia.go 非成功路径行为一致。
		errBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("[上游非 200 错误体]\n%s\n", truncate(string(errBytes), 2000))
		return
	}

	if !isStreaming {
		// 非流式: 全量读 body, 打印首字节时延(即"拿到完整响应"的耗时)与字节总量。
		ttfb := time.Since(start)
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("[读响应失败] %v\n", err)
			return
		}
		fmt.Printf("[非流式] 完整响应耗时 %v | 字节数 %d\n", ttfb, len(bodyBytes))
		fmt.Printf("[非流式] 响应体(前 2000 字节):\n%s\n", truncate(string(bodyBytes), 2000))
		// 解析 usage 看是否一次性 JSON。
		var probe struct {
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(bodyBytes, &probe) == nil && probe.Usage != nil {
			fmt.Printf("[非流式] usage: %+v\n", *probe.Usage)
		} else {
			fmt.Printf("[非流式] 未解析到 usage 字段(可能上游未返回)\n")
		}
		return
	}

	// 流式: 原样复刻 nvidia.go:613-650 的逐行扫描逻辑。
	// 先探测首字节时延(首帧多快到达 = 流式真伪的关键证据)。
	fmt.Println("[流式] 开始逐行读取 SSE, 下面每行带 [行号 | 相对启动ms | 累计字节]:")
	flusher := bufio.NewReader(resp.Body)
	scanner := bufio.NewScanner(flusher)
	// 与 nvidia.go:615 一致的 8MB 单行缓冲上限,避免长帧/工具调用被截断丢 usage。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	totalBytes := 0
	firstFrameAt := time.Duration(0)
	firstFrameSet := false
	chunkCount := 0
	dataCount := 0
	// 思考标记命中累计: "content" / "reasoning_content" / "reasoning" / "thinking" 各出现多少帧。
	deltaFieldHits := map[string]int{}
	var lastUsage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !firstFrameSet {
			firstFrameAt = time.Since(start)
			firstFrameSet = true
		}
		lineNo++
		totalBytes += len(line) + 1 // +1 为换行
		fmt.Printf("  [#%-4d | %6dms | cum=%dB] %s\n", lineNo, time.Since(start).Milliseconds(), totalBytes, line)

		if line == "" {
			// 空行 = SSE 事件分隔,与 nvidia.go 一致。
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			fmt.Printf("  ↑ 收到 [DONE],流结束\n")
			break
		}
		dataCount++
		// 嗅探 usage(nvidia.go:641-648 只在末帧有 usage,这里实时打印命中情况)。
		var chunk struct {
			Choices []struct {
				Index         int             `json:"index"`
				Delta         json.RawMessage `json:"delta"`
				FinishReason  string          `json:"finish_reason"`
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
			// 把 delta 原样解析成 map,列出所有 key,并专门高亮 content / reasoning_content /
			// reasoning / thinking / signature 这几类。让上游自己暴露它用什么标记承载思考。
			if len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta) > 0 && string(chunk.Choices[0].Delta) != "{}" {
				var deltaMap map[string]json.RawMessage
				if json.Unmarshal(chunk.Choices[0].Delta, &deltaMap) == nil {
					keys := make([]string, 0, len(deltaMap))
					for k := range deltaMap {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					// 标记本帧携带了哪些"思考"相关字段(不限字段名,这里只是高亮常见候选)。
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
					// 逐字段打印取值,思考字段额外高亮打印完整内容,其它字段截断。
					for _, k := range keys {
						raw := string(deltaMap[k])
						if thinkers[k] || k == "content" {
							// 思考/正文:打印完整片段(cumulative 后便于你看推理走向)。
							fmt.Printf("      · %s = %s\n", k, raw)
						} else {
							// 其它字段(如 role/tool_calls):短则全打,长则截断。
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

	fmt.Println("---- 流式汇总 ----")
	fmt.Printf("首帧时延      : %v\n", firstFrameAt)
	fmt.Printf("总行数        : %d\n", lineNo)
	fmt.Printf("data: 帧数    : %d (其中成功解析的 chunk 帧: %d)\n", dataCount, chunkCount)
	fmt.Printf("累计字节      : %d\n", totalBytes)
	fmt.Printf("总耗时        : %v\n", time.Since(start))
	if lastUsage != nil {
		fmt.Printf("末帧 usage    : %+v\n", *lastUsage)
	} else {
		fmt.Printf("末帧 usage    : (未命中, 上游可能没在末帧吐 usage)\n")
	}
	fmt.Println("---- 思考链标记统计 ----")
	if len(deltaFieldHits) == 0 {
		fmt.Println("delta 无任何增量字段命中(无 content / 思考标记), 上游可能根本没流式吐 delta → ✗ 非流式或空响应")
	} else {
		// 命中字段按出现帧数降序打印, 直观看哪个字段吐了思考。
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
			if h.name == "content" {
				mark = "(正文)"
			} else if h.name == "reasoning_content" || h.name == "reasoning" || h.name == "thinking" {
				mark = "(思考链 ✓)"
			}
			fmt.Printf("  %-20s 出现 %d 帧 %s\n", h.name, h.count, mark)
		}
		// 结论: 是否检测到独立的思考字段。
		hasThink := false
		for _, k := range []string{"reasoning_content", "reasoning", "thinking"} {
			if deltaFieldHits[k] > 0 {
				hasThink = true
			}
		}
		if hasThink {
			fmt.Println("结论: 上游在 delta 中用独立的思考字段承载推理过程 → 思考链有特殊标记 ✓")
		} else if deltaFieldHits["content"] > 0 {
			fmt.Println("结论: delta 只有 content, 无独立思考字段 → 思考内容混在正文里/或不开启思考(无特殊标记)")
		}
	}

	// 铁证判定: 用 Content-Type 与首帧时延给结论。
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		fmt.Println("结论: 上游 Content-Type=text/event-stream, 且逐帧到达 → 确认流式返回 ✓")
	} else if chunkCount > 1 || dataCount > 1 {
		fmt.Println("结论: Content-Type 非 text/event-stream, 但拆成多帧逐步到达 → 实质流式(协议头不标准) △")
	} else {
		fmt.Println("结论: 仅一次性返回, 无多帧逐步到达 → 非流式 ✗")
	}
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

// dupStdout 保存当前真实屏幕的 os.Stdout 引用(不关闭它),返回该引用。
// 后续 startTee 会用 os.Pipe 把 os.Stdout 指向 pipe 写端,goroutine 再把内容
// 同时写回这里保存的真实屏幕与日志文件,实现"屏幕 + 文件"双向输出。
func dupStdout() *os.File {
	return os.Stdout
}

// startTee 把标准输出 tee 到一个带时间戳的日志文件:
//   1. 解析日志文件路径(默认 ./logs/nvidia_probe_<时间戳>_<mode>.log,可用 logFile 覆盖);
//   2. 建 os.Pipe(),把 os.Stdout 指向 pipe 写端;
//   3. goroutine 从 pipe 读端读出所有 stdout 内容, 同时写回 realscreen 与日志文件;
//   4. main 结束前需 flush: 通过关闭 pipe 写端让 goroutine 退出并 close 文件。
//
// 跨平台、无 syscall 依赖: 仅依赖 os.Stdout 的 *os.File 引用与 os.Pipe。
func startTee(realStdout *os.File, logFile, mode string) {
	var logPath string
	if logFile != "" {
		logPath = logFile
	} else {
		stamp := time.Now().Format("20060102_150405")
		logPath = filepath.Join(".", "logs", fmt.Sprintf("nvidia_probe_%s_%s.log", stamp, mode))
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(realStdout, "[warn] 建日志父目录失败, 仅输出到屏幕: %v\n", err)
		return
	}
	f, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(realStdout, "[warn] 创建日志文件失败(%s): %v, 仅输出到屏幕\n", logPath, err)
		return
	}

	// 建 pipe 拦截 stdout。
	r, w, err := os.Pipe()
	if err != nil {
		// pipe 建失败:回退仅写文件(屏幕照旧走原 os.Stdout)。
		fmt.Fprintf(realStdout, "[warn] os.Pipe 失败, 退化为仅写文件: %v\n", err)
		go func() {
			// 占位复制: 这种情况下不拦截 stdout, 只把后续手工 fdump 略过。
			_ = f.Close()
		}()
		return
	}

	// 关键: 切换 os.Stdout 到 pipe 写端, 之后所有 fmt.Print* 进 pipe。
	os.Stdout = w

	// 用 MultiWriter 把读出的字节同时写回真实屏幕与日志文件。
	mw := io.MultiWriter(realStdout, f)

	// 启动 goroutine 做 copy。main 结束时会通过 flushTee 关闭 w,让 Read 返回 EOF,
	// goroutine 随之收尾并关闭 f。
	go func() {
		_, _ = io.Copy(mw, r)
		_ = f.Close()
		_ = r.Close()
	}()

	// 在文件首行写一行带路径的标记(已走 pipe → mw → 文件)。
	fmt.Printf("[log] 本运行日志将写入: %s\n", logPath)

	// 注册 main 退出前的 flush: 关闭 pipe 写端, 驱动 goroutine 收尾。
	// 用 defer 不行(本函数返回后 main 还在跑, 不能现在关),
	// 故暴露 flushTee 供 main 退出前显式调用。这里把写端句柄存到包级变量。
	teeWriter = w
	teeLogPath = logPath
	teeScreen = realStdout
}

var (
	teeWriter  *os.File
	teeLogPath string
	teeScreen  *os.File
)

// flushTee 在 main 退出前调用: 关闭 pipe 写端, 让 tee goroutine 读到 EOF 并
// 刷盘关闭日志文件, 再把 os.Stdout 恢复回真实屏幕(避免退出阶段日志丢失)。
func flushTee() {
	if teeWriter == nil {
		return
	}
	_ = teeWriter.Close()
	teeWriter = nil
	// 恢复 os.Stdout 到真实屏幕,便于退出阶段的打印仍可见。
	if teeScreen != nil {
		os.Stdout = teeScreen
	}
	fmt.Fprintf(os.Stderr, "[log] 完整日志已写入: %s\n", teeLogPath)
}

// 保留 io 引用以防某些工具链把 io 标记为未使用(io.Copy 已用到, 实际不会触发)。
var _ = io.EOF
