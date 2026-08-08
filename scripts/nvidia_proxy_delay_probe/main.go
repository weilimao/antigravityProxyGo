// Command nvidia_proxy_delay_probe 用于对比直连 NVIDIA 上游与通过代理 Endpoint（如 Cloudflare Workers 代理）的响应延迟与性能差异。
//
// 本探针已集成 NVIDIA NIM 思考模式（Thinking Level）与 `-verbose` 实时流式吐字刷屏支持，
// 可以在测试过程中实时向控制台打印 AI 思考链 (Reasoning Process) 与回答正文的流式字块及毫秒级到达时间。
//
// 用法示例:
//
//	go run ./scripts/nvidia_proxy_delay_probe -proxy-url="https://your-worker.subdomain.workers.dev/v1"
//	go run ./scripts/nvidia_proxy_delay_probe -proxy-url="https://your-worker.subdomain.workers.dev/v1" -verbose=true
//	go run ./scripts/nvidia_proxy_delay_probe -proxy-url="https://your-worker.subdomain.workers.dev/v1" -thinking=true -reasoning-effort=high
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
	"strings"
	"sync"
	"time"
)

const (
	defaultDirectURL = "https://integrate.api.nvidia.com/v1"
	defaultModel     = "z-ai/glm-5.2"
	defaultPrompt    = `分析下面这段 Go 代码的并发安全问题,详述你的推理过程,最后给结论。

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
	hardcodedToken = ""
)

type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIChatRequest struct {
	Model              string                 `json:"model"`
	Messages           []ChatMessage          `json:"messages"`
	Stream             bool                   `json:"stream,omitempty"`
	StreamOptions      *ChatStreamOptions     `json:"stream_options,omitempty"`
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
}

type ProbeResult struct {
	Name            string
	URL             string
	IsStream        bool
	StatusCode      int
	TTFT            time.Duration // 收到第一包 SSE data 的总时间
	ThinkTTFT       time.Duration // 收到第一帧思考链 (reasoning_content/reasoning) 的时间
	ContentTTFT     time.Duration // 收到第一帧正式回答 (content) 的时间
	TotalTime       time.Duration
	TotalBytes      int
	ChunkCount      int
	DataCount       int
	ReasoningFrames int
	ContentFrames   int
	AvgInterval     time.Duration
	MaxInterval     time.Duration
	ContentType     string
	Err             error
	UsageTokens     int
}

func main() {
	directURL := flag.String("direct-url", defaultDirectURL, "NVIDIA 官方直连 BaseURL (默认 https://integrate.api.nvidia.com/v1)")
	proxyURL := flag.String("proxy-url", "", "代理 Endpoint BaseURL (例如 https://your-worker.workers.dev/v1)")
	token := flag.String("token", hardcodedToken, "NVIDIA AccessToken (默认硬编码或从 .secret_token 读取)")
	model := flag.String("model", defaultModel, "测试模型 ID (默认 z-ai/glm-5.2)")
	prompt := flag.String("prompt", defaultPrompt, "测试 Prompt (默认使用 Go 代码并发安全问题)")
	thinking := flag.Bool("thinking", true, "是否开启思考模式 (注入 chat_template_kwargs: {thinking: true})")
	reasoningEffort := flag.String("reasoning-effort", "high", "思考等级: high | max")
	verbose := flag.Bool("verbose", true, "是否实时流式打字输出日志 (开启后可在控制台看到实时思考链与正文吐字过程)")
	mode := flag.String("mode", "both", "探测模式: both (流式+非流式) | stream (仅流式) | nostream (仅非流式)")
	logFile := flag.String("log-file", "", "日志输出文件路径(留空自动生成到 ./logs/)")
	flag.Parse()

	activeToken := resolveToken(*token)
	if activeToken == "" {
		fmt.Println("[警告] 未检测到有效的 Token！请通过 -token 传参或在 scripts/.secret_token 中配置。")
	}

	realStdout := dupStdout()
	startTee(realStdout, *logFile, *mode)

	fmt.Println("=========================================================================")
	fmt.Println("       NVIDIA API 直连 vs 代理 (Worker) 思考模式延迟对比探针             ")
	fmt.Println("=========================================================================")
	fmt.Printf("官方直连 URL   : %s\n", *directURL)
	if *proxyURL != "" {
		fmt.Printf("代理 Endpoint    : %s\n", *proxyURL)
	} else {
		fmt.Println("代理 Endpoint    : [未提供 -proxy-url 标志，仅探测直连]")
	}
	fmt.Printf("模型 ID          : %s\n", *model)
	fmt.Printf("思考开关         : %v (effort=%s)\n", *thinking, *reasoningEffort)
	fmt.Printf("实时流式打字     : %v (-verbose 开关)\n", *verbose)
	fmt.Printf("Token 掩码       : %s...\n", safePrefix(activeToken, 14))
	fmt.Printf("探测模式         : %s\n", *mode)
	fmt.Printf("测试 Prompt 预览 : %q\n", truncate(*prompt, 80))
	fmt.Println("=========================================================================")

	var results []ProbeResult

	streamModes := []bool{}
	switch *mode {
	case "stream":
		streamModes = append(streamModes, true)
	case "nostream":
		streamModes = append(streamModes, false)
	case "both":
		streamModes = append(streamModes, false, true)
	default:
		fmt.Printf("[错误] 未知的 mode: %s，允许取值: both|stream|nostream\n", *mode)
		flushTee()
		return
	}

	for _, isStream := range streamModes {
		streamLabel := "流式 (stream=true)"
		if !isStream {
			streamLabel = "非流式 (stream=false)"
		}

		fmt.Printf(">>>>>>> 开始测试轮次: %s <<<<<<<\n\n", streamLabel)

		// 1. 直连官方测试
		fmt.Println("---- [1/2] 官方直连 (Direct) 探测中 ----")
		targetDirect := buildChatEndpoint(*directURL)
		resDirect := runProbe("官方直连", targetDirect, activeToken, *model, *prompt, *thinking, *reasoningEffort, isStream, *verbose)
		results = append(results, resDirect)
		printSingleSummary(resDirect)
		fmt.Println()

		// 2. 代理地址测试 (若提供)
		if *proxyURL != "" {
			fmt.Println("---- [2/2] 代理 Endpoint (Proxy) 探测中 ----")
			targetProxy := buildChatEndpoint(*proxyURL)
			resProxy := runProbe("代理 Endpoint", targetProxy, activeToken, *model, *prompt, *thinking, *reasoningEffort, isStream, *verbose)
			results = append(results, resProxy)
			printSingleSummary(resProxy)
			fmt.Println()
		}

		time.Sleep(1 * time.Second)
	}

	// 打印对比总结研判报告
	printComparisonReport(results)

	flushTee()
}

func buildChatEndpoint(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/chat/completions"
	}
	return trimmed + "/v1/chat/completions"
}

func runProbe(label, targetURL, token, model, prompt string, enableThinking bool, reasoningEffort string, isStreaming, verbose bool) ProbeResult {
	res := ProbeResult{
		Name:     label,
		URL:      targetURL,
		IsStream: isStreaming,
	}

	chatReq := &OpenAIChatRequest{
		Model:    model,
		Messages: []ChatMessage{{Role: "user", Content: prompt}},
		Stream:   isStreaming,
	}
	if isStreaming {
		chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
	if enableThinking {
		chatReq.ChatTemplateKwargs = map[string]interface{}{
			"thinking":         true,
			"reasoning_effort": reasoningEffort,
		}
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		res.Err = fmt.Errorf("序列化请求体失败: %w", err)
		return res
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		res.Err = fmt.Errorf("构造 HTTP 请求失败: %w", err)
		return res
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	var client *http.Client
	if isStreaming {
		// 流式思考模式耗时较长，Timeout 设为 0 避免客户端超时提前掐断
		client = &http.Client{Timeout: 0}
	} else {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		res.Err = fmt.Errorf("网络请求失败 (耗时 %v): %w", time.Since(start), err)
		return res
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	res.ContentType = resp.Header.Get("Content-Type")

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		res.TotalTime = time.Since(start)
		res.Err = fmt.Errorf("上游状态码非 200 [%d]: %s", resp.StatusCode, truncate(string(errBytes), 500))
		return res
	}

	if !isStreaming {
		bodyBytes, err := io.ReadAll(resp.Body)
		res.TotalTime = time.Since(start)
		res.TTFT = res.TotalTime
		if err != nil {
			res.Err = fmt.Errorf("读取非流式响应体失败: %w", err)
			return res
		}
		res.TotalBytes = len(bodyBytes)

		var probe struct {
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(bodyBytes, &probe) == nil && probe.Usage != nil {
			res.UsageTokens = probe.Usage.TotalTokens
		}
		return res
	}

	// 流式读取 SSE
	flusher := bufio.NewReader(resp.Body)
	scanner := bufio.NewScanner(flusher)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	firstFrameSet := false
	var lastFrameTime time.Time
	var totalIntervals time.Duration
	intervalCount := 0

	if verbose {
		fmt.Printf("[%s SSE 实时流输出开始]:\n", label)
	}

	for scanner.Scan() {
		now := time.Now()
		line := scanner.Text()
		res.TotalBytes += len(scanner.Bytes()) + 1

		if !firstFrameSet && strings.HasPrefix(line, "data:") {
			res.TTFT = now.Sub(start)
			firstFrameSet = true
			lastFrameTime = now
		} else if firstFrameSet && strings.HasPrefix(line, "data:") {
			interval := now.Sub(lastFrameTime)
			totalIntervals += interval
			intervalCount++
			if interval > res.MaxInterval {
				res.MaxInterval = interval
			}
			lastFrameTime = now
		}

		res.ChunkCount++
		if strings.HasPrefix(line, "data:") {
			res.DataCount++
			dataContent := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataContent != "[DONE]" {
				// 尝试解析 JSON 抓取思考链与正文
				var sChunk struct {
					Choices []struct {
						Delta struct {
							ReasoningContent string `json:"reasoning_content"`
							Reasoning        string `json:"reasoning"`
							Thinking         string `json:"thinking"`
							Content          string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
					Usage *struct {
						TotalTokens int `json:"total_tokens"`
					} `json:"usage"`
				}

				if json.Unmarshal([]byte(dataContent), &sChunk) == nil {
					if sChunk.Usage != nil {
						res.UsageTokens = sChunk.Usage.TotalTokens
					}
					if len(sChunk.Choices) > 0 {
						delta := sChunk.Choices[0].Delta
						reasoningText := delta.ReasoningContent
						if reasoningText == "" {
							reasoningText = delta.Reasoning
						}
						if reasoningText == "" {
							reasoningText = delta.Thinking
						}

						if reasoningText != "" {
							res.ReasoningFrames++
							if res.ThinkTTFT == 0 {
								res.ThinkTTFT = now.Sub(start)
							}
							if verbose {
								fmt.Printf(" [🧠思考|+%.1fs] %s\n", now.Sub(start).Seconds(), escapeNewlines(reasoningText))
							}
						}
						if delta.Content != "" {
							res.ContentFrames++
							if res.ContentTTFT == 0 {
								res.ContentTTFT = now.Sub(start)
							}
							if verbose {
								fmt.Printf(" [💬正文|+%.1fs] %s\n", now.Sub(start).Seconds(), escapeNewlines(delta.Content))
							}
						}
					}
				}
			} else {
				if verbose {
					fmt.Printf(" [🏁结束|+%.1fs] SSE [DONE]\n", now.Sub(start).Seconds())
				}
			}
		}
	}

	if verbose {
		fmt.Printf("[%s SSE 实时流输出结束]\n\n", label)
	}

	res.TotalTime = time.Since(start)

	if intervalCount > 0 {
		res.AvgInterval = totalIntervals / time.Duration(intervalCount)
	}

	if err := scanner.Err(); err != nil {
		res.Err = fmt.Errorf("SSE 流扫描解析出错: %w", err)
	}

	return res
}

func escapeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "\\n")
}

func printSingleSummary(r ProbeResult) {
	fmt.Printf("[%s] 测试汇总:\n", r.Name)
	if r.Err != nil {
		fmt.Printf("  ❌ 状态: 失败 (%v)\n", r.Err)
		return
	}
	fmt.Printf("  ✅ HTTP 状态码     : %d\n", r.StatusCode)
	fmt.Printf("  📄 Content-Type    : %s\n", r.ContentType)
	if r.IsStream {
		fmt.Printf("  ⚡ SSE 首帧到达耗时 : %v\n", r.TTFT.Round(time.Millisecond))
		if r.ThinkTTFT > 0 {
			fmt.Printf("  🧠 思考首帧 (Think)  : %v\n", r.ThinkTTFT.Round(time.Millisecond))
		}
		if r.ContentTTFT > 0 {
			fmt.Printf("  💬 正文首帧 (Content): %v\n", r.ContentTTFT.Round(time.Millisecond))
		}
		fmt.Printf("  ⏱️  帧均间隔 / 最大  : %v / %v\n", r.AvgInterval.Round(time.Millisecond), r.MaxInterval.Round(time.Millisecond))
		fmt.Printf("  📦 帧总数 (思考/正文): %d 帧 (思考: %d 帧 | 正文: %d 帧)\n", r.DataCount, r.ReasoningFrames, r.ContentFrames)
	} else {
		fmt.Printf("  ⏱️  全量响应时间     : %v\n", r.TotalTime.Round(time.Millisecond))
	}
	fmt.Printf("  📊 接收字节数       : %d 字节\n", r.TotalBytes)
	if r.UsageTokens > 0 {
		fmt.Printf("  🔢 消耗 TotalToken   : %d\n", r.UsageTokens)
	}
}

func printComparisonReport(results []ProbeResult) {
	fmt.Println("\n=========================================================================")
	fmt.Println("                     思考模式下延迟与性能研判分析报告                    ")
	fmt.Println("=========================================================================")

	if len(results) == 0 {
		fmt.Println("未搜集到任何探测数据。")
		return
	}

	groups := map[bool][]ProbeResult{}
	for _, r := range results {
		groups[r.IsStream] = append(groups[r.IsStream], r)
	}

	for isStream, list := range groups {
		modeStr := "【流式 (stream=true)】"
		if !isStream {
			modeStr = "【非流式 (stream=false)】"
		}
		fmt.Println(modeStr)

		var directRes, proxyRes *ProbeResult
		for i := range list {
			if strings.Contains(list[i].Name, "直连") {
				directRes = &list[i]
			} else {
				proxyRes = &list[i]
			}
		}

		if directRes != nil {
			printResRow("官方直连", directRes)
		}
		if proxyRes != nil {
			printResRow("代理 Endpoint", proxyRes)
		}

		if directRes != nil && proxyRes != nil && directRes.Err == nil && proxyRes.Err == nil {
			diffTTFT := proxyRes.TTFT - directRes.TTFT
			diffTotal := proxyRes.TotalTime - directRes.TotalTime

			fmt.Println("\n  💡 对比分析结论:")
			if isStream {
				if diffTTFT > 0 {
					fmt.Printf("     • 首包延迟 (TTFT) : 代理慢了 +%v\n", diffTTFT.Round(time.Millisecond))
				} else {
					fmt.Printf("     • 首包延迟 (TTFT) : 代理快了 %v\n", (-diffTTFT).Round(time.Millisecond))
				}
				if proxyRes.ThinkTTFT > 0 && directRes.ThinkTTFT > 0 {
					diffThink := proxyRes.ThinkTTFT - directRes.ThinkTTFT
					fmt.Printf("     • 思考首帧 (Think): 差值 %v\n", diffThink.Round(time.Millisecond))
				}
				if proxyRes.ReasoningFrames != directRes.ReasoningFrames {
					fmt.Printf("     ⚠️ 思考帧数不一致 (直连: %d 帧 vs 代理: %d 帧)，代理层可能篡改或合流了思考数据包！\n", directRes.ReasoningFrames, proxyRes.ReasoningFrames)
				}
			}
			if diffTotal > 0 {
				fmt.Printf("     • 传输总时长        : 代理慢了 +%v\n", diffTotal.Round(time.Millisecond))
			} else {
				fmt.Printf("     • 传输总时长        : 代理快了 %v\n", (-diffTotal).Round(time.Millisecond))
			}
			if isStream && proxyRes.AvgInterval > directRes.AvgInterval*2 {
				fmt.Println("     ⚠️ 发现代理层的帧间隔显著变大，说明代理服务器可能开启了响应缓冲 (Buffering)。")
			}
		}
		fmt.Println("-------------------------------------------------------------------------")
	}
}

func printResRow(label string, r *ProbeResult) {
	if r.Err != nil {
		fmt.Printf("  %-15s | ❌ 请求失败 | %v\n", label, r.Err)
		return
	}
	if r.IsStream {
		fmt.Printf("  %-15s | 状态:%d | TTFT:%-7v | 思考首帧:%-7v | 正文首帧:%-7v | 总耗时:%-8v | 字节:%d\n",
			label, r.StatusCode, r.TTFT.Round(time.Millisecond), r.ThinkTTFT.Round(time.Millisecond), r.ContentTTFT.Round(time.Millisecond), r.TotalTime.Round(time.Millisecond), r.TotalBytes)
	} else {
		fmt.Printf("  %-15s | 状态:%d | 总耗时:%-8v | 字节:%d\n",
			label, r.StatusCode, r.TotalTime.Round(time.Millisecond), r.TotalBytes)
	}
}

func resolveToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	if envToken := os.Getenv("NVIDIA_API_KEY"); envToken != "" {
		return envToken
	}
	secretPath := filepath.Join("scripts", ".secret_token")
	if data, err := os.ReadFile(secretPath); err == nil {
		tok := strings.TrimSpace(string(data))
		if tok != "" {
			return tok
		}
	}
	return ""
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... [截断]"
}

// Tee 输出控制
var (
	stdoutWriter io.Writer = os.Stdout
	teeFile      *os.File
	origStdout   *os.File
	teeWg        sync.WaitGroup
	pipeR        *os.File
	pipeW        *os.File
)

func dupStdout() *os.File {
	return os.Stdout
}

func startTee(realStdout *os.File, logPath, mode string) {
	if logPath == "" {
		os.MkdirAll("logs", 0755)
		logPath = filepath.Join("logs", fmt.Sprintf("nvidia_proxy_probe_%s_%s.log", time.Now().Format("20060102_150405"), mode))
	} else {
		os.MkdirAll(filepath.Dir(logPath), 0755)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Printf("[日志记录警告] 无法创建日志文件 %s: %v\n", logPath, err)
		return
	}
	teeFile = f
	origStdout = realStdout

	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	pipeR = r
	pipeW = w
	os.Stdout = w

	teeWg.Add(1)
	go func() {
		defer teeWg.Done()
		mw := io.MultiWriter(realStdout, teeFile)
		io.Copy(mw, pipeR)
	}()

	fmt.Printf("[日志开启] 输出同步保存至文件: %s\n\n", logPath)
}

func flushTee() {
	if pipeW != nil {
		pipeW.Close()
	}
	teeWg.Wait()
	if pipeR != nil {
		pipeR.Close()
	}
	if teeFile != nil {
		teeFile.Close()
	}
	if origStdout != nil {
		os.Stdout = origStdout
	}
}
