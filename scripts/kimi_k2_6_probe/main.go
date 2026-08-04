// Command kimi_k2_6_probe 按 NVIDIA 官方示例精确复刻请求体。
//
// 官方示例(Streaming):
//
//	invoke_url = "https://integrate.api.nvidia.com/v1/chat/completions"
//	headers = {
//	  "Authorization": "Bearer $NVIDIA_API_KEY",
//	  "Accept": "text/event-stream",
//	}
//	payload = {
//	  "messages": [{"role":"user","content":"..."}],
//	  "model": "moonshotai/kimi-k2.6",
//	  "max_tokens": 16384,
//	  "seed": 0,
//	  "stream": True,
//	  "temperature": 1,
//	  "top_p": 1,
//	}
//
// 用法:
//
//	go run ./scripts/kimi_k2_6_probe
//	go run ./scripts/kimi_k2_6_probe -mode stream
//	go run ./scripts/kimi_k2_6_probe -mode nostream
//	go run ./scripts/kimi_k2_6_probe -model deepseek-ai/deepseek-v4-pro
//	go run ./scripts/kimi_k2_6_probe -token nvapi-XXXX
//	go run ./scripts/kimi_k2_6_probe -prompt "你是谁"
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
	"time"
)

// ===== 请求体:与官方示例 payload 逐字段完全一致 =====
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Payload struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Seed        int       `json:"seed"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature"`
	TopP        float64   `json:"top_p"`
}

// 默认值(与官方示例一致)。
const (
	defaultBaseURL = "https://integrate.api.nvidia.com/v1"
	defaultModel   = "moonshotai/kimi-k2.6"
	defaultPrompt  = "用一句话介绍你自己,并说明你是哪个模型。"
	// 硬编码占位:请勿填入真实 Key。
	hardcodedToken = ""
)

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "NVIDIA 上游 BaseURL")
	token := flag.String("token", hardcodedToken, "NVIDIA Key(三级回退: flag > env NVIDIA_TOKEN > scripts/.secret_token)")
	model := flag.String("model", defaultModel, "上游模型 ID")
	prompt := flag.String("prompt", defaultPrompt, "测试 prompt")
	mode := flag.String("mode", "both", "both | nostream | stream")
	logFile := flag.String("log-file", "", "日志文件路径(自动落到 ./logs/)")
	flag.Parse()

	activeToken := resolveToken(*token)

	// tee stdout → 屏幕 + 日志文件。
	realStdout := dupStdout()
	startTee(realStdout, *logFile, *mode)

	// 拼 URL: {BaseURL}/v1/chat/completions。
	trimmed := strings.TrimRight(*baseURL, "/")
	chatURL := trimmed + "/v1/chat/completions"
	if strings.HasSuffix(trimmed, "/v1") {
		chatURL = trimmed + "/chat/completions"
	}

	fmt.Printf("==== kimi-k2.6 实测 (%s) ====\n", *mode)
	fmt.Printf("Endpoint : %s\n", chatURL)
	fmt.Printf("Model   : %s\n", *model)
	fmt.Printf("Prompt  : %q\n", *prompt)
	fmt.Printf("Token   : %s...\n", safePrefix(activeToken, 14))
	fmt.Println()

	switch *mode {
	case "nostream":
		run(chatURL, activeToken, *model, *prompt, false)
	case "stream":
		run(chatURL, activeToken, *model, *prompt, true)
	case "both":
		fmt.Println("──── ① 非流式 ────")
		run(chatURL, activeToken, *model, *prompt, false)
		fmt.Println()
		fmt.Println("──── ② 流式 ────")
		run(chatURL, activeToken, *model, *prompt, true)
	default:
		fmt.Printf("未知 mode: %s (允许 both | nostream | stream)\n", *mode)
	}

	flushTee()
}

func run(chatURL, token, model, prompt string, stream bool) {
	tag := "nostream"
	accept := "application/json"
	if stream {
		tag = "stream"
		accept = "text/event-stream"
	}

	payload := Payload{
		Model:       model,
		Messages:    []Message{{Role: "user", Content: prompt}},
		MaxTokens:   16384,
		Seed:        0,
		Stream:      stream,
		Temperature: 1,
		TopP:        1,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, chatURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)

	start := time.Now()
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ 请求失败 (%v) | %v\n", time.Since(start), err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("[%s] 状态码=%d | Content-Type=%q | %v\n", tag, resp.StatusCode, resp.Header.Get("Content-Type"), time.Since(start))

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Printf("[错误体] %s\n", b)
		return
	}

	if !stream {
		// 非流式:全量一次性 JSON。
		b, _ := io.ReadAll(resp.Body)
		fmt.Printf("[%s] 完整响应 (%d bytes, %v):\n", tag, len(b), time.Since(start))
		// 只解最外层两个关键字段,其余原样打印不丢信息。
		var probe struct {
			Choices []struct {
				Message      Message `json:"message"`
				FinishReason string  `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(b, &probe) == nil {
			for _, c := range probe.Choices {
				fmt.Printf("  finish_reason = %s\n", c.FinishReason)
				fmt.Printf("  content = %s\n", c.Message.Content)
			}
			if probe.Usage != nil {
				fmt.Printf("  usage = prompt=%d, completion=%d, total=%d\n", probe.Usage.PromptTokens, probe.Usage.CompletionTokens, probe.Usage.TotalTokens)
			}
		} else {
			fmt.Printf("%s\n", b)
		}
		return
	}

	// 流式:逐行 SSE,与 Python iter_lines 行为一致。
	fmt.Printf("[%s] 逐行 SSE:\n", tag)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	firstFrameAt := time.Duration(0)
	firstFrameSet := false
	lineNo := 0
	chunkNo := 0
	var contentAccum strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		if line == "" {
			continue
		}
		if !firstFrameSet {
			firstFrameAt = time.Since(start)
			firstFrameSet = true
		}
		if strings.HasPrefix(line, "data:") {
			dataVal := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataVal == "[DONE]" {
				fmt.Printf("  [L%04d | %dms] data: [DONE]\n", lineNo, time.Since(start).Milliseconds())
				break
			}
			chunkNo++
			// 解析 delta 中 content 字段做累积正文。
			var frame struct {
				Choices []struct {
					Delta        map[string]json.RawMessage `json:"delta"`
					FinishReason string                    `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(dataVal), &frame) == nil {
				// 打印摘要: 每一帧至少一行;内容流再选择性截断。
				parts := []string{}
				for _, torch := range frame.Choices {
					if torch.FinishReason != "" {
						parts = append(parts, "finish="+torch.FinishReason)
					}
					if len(torch.Delta) > 0 {
						var c string
						if raw, ok := torch.Delta["content"]; ok {
							if json.Unmarshal(raw, &c) == nil && c != "" {
								contentAccum.WriteString(c)
							}
						}
					}
				}
				usagePart := ""
				if frame.Usage != nil {
					usagePart = fmt.Sprintf(" usage=%d/%d/%d", frame.Usage.PromptTokens, frame.Usage.CompletionTokens, frame.Usage.TotalTokens)
				}
				label := strings.Join(parts, ", ")
				if label != "" {
					fmt.Printf("  #%04d ch=%04d | %dms | %s%s\n", lineNo, chunkNo, time.Since(start).Milliseconds(), label, usagePart)
				}
			} else {
				if chunkNo <= 3 {
					fmt.Printf("  #%04d ch=%04d | %s...(parse fail)\n", lineNo, chunkNo, truncate(dataVal, 120))
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("  ❌ 扫描出错: %v\n", err)
	}

	fmt.Println()
	fmt.Println("──── 汇总 ────")
	fmt.Printf("  首帧=%v 帧=%d 总耗时=%v\n", firstFrameAt, chunkNo, time.Since(start))
	if contentAccum.Len() > 0 {
		fmt.Printf("  回复: %s\n", contentAccum.String())
	}
}

// ===== 工具函数 =====

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

// ===== tee 日志 =====

func dupStdout() *os.File { return os.Stdout }

var (
	teeWriter  *os.File
	teeLogPath string
	teeScreen  *os.File
)

func startTee(realStdout *os.File, logFile, mode string) {
	var logPath string
	if logFile != "" {
		logPath = logFile
	} else {
		stamp := time.Now().Format("20060102_150405")
		logPath = filepath.Join(".", "logs", fmt.Sprintf("kimi_probe_%s_%s.log", stamp, mode))
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(realStdout, "[warn] 建日志父目录失败: %v\n", err)
		return
	}
	f, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(realStdout, "[warn] 创建日志文件失败 (%s): %v\n", logPath, err)
		return
	}
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(realStdout, "[warn] os.Pipe 失败: %v\n", err)
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
	fmt.Printf("[log] 日志将写入: %s\n", logPath)
	teeWriter = w
	teeLogPath = logPath
	teeScreen = realStdout
}

func flushTee() {
	if teeWriter == nil {
		return
	}
	_ = teeWriter.Close()
	teeWriter = nil
	if teeScreen != nil {
		os.Stdout = teeScreen
	}
	fmt.Fprintf(os.Stderr, "[log] 完整日志已写入: %s\n", teeLogPath)
}

var _ = io.EOF