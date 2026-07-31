// Command antigravity_relay_stream_probe 直连中继分发服务(默认 172.16.10.114:18444),
// 走 antigravity 号池的 v1internal 接口(/v1internal:streamGenerateContent?alt=sse),
// 逐帧打印上游 Gemini 原生 SSE,专项核对:响应里到底带不带思考链(thought / thoughtSignature /
// thoughtSummaryText 等字段)、thoughtsTokenCount 计费、finishReason 走向。
//
// 与已有两个 NVIDIA 探针的区别:
//   - scripts/nvidia_upstream_stream_probe:探 NVIDIA 上游流式真伪 + gzip 攒批指纹;
//   - scripts/nvidia_thinking_level_probe:探 NVIDIA NIM chat_template_kwargs 思考等级注入;
//   - 这个探"中继 v1internal 入口 → antigravity 号池 → 上游 Gemini SSE 思考链字段"。
//
// 不 import internal/relay(避免拖中继依赖),只复刻 v1internal 请求包子集字段。
// 不写统计文件,只打真实中继一次流式调用(stream / nostream 可选),日志落盘。
//
// 链路事实锚定:
//   - 入口路由 internal/relay/server.go:62,/v1internal: 前缀进 compatHandler;
//   - 鉴权 internal/relay/compat.go:113 + auth.go:75,sk-ant- 前缀 key 自动 bypass 到启用用户;
//   - v1internal 处理 internal/relay/compat.go:1635,自动补 project/requestId,判流式;
//   - antigravity 通道 internal/relay/compat.go:1807,发往 daily-cloudcode-pa.googleapis.com/v1internal:%s;
//   - 流式回传 internal/relay/compat.go:1925,上游 SSE 原样透传(4KB 分块 flush + 提取 thoughtSignature)。
// 故本脚本打 18444 拿到的就是 Gemini 原生 SSE: data: {"response":{"candidates":[...]}}.
//
// 用法:
//
//	go run ./scripts/antigravity_relay_stream_probe -key sk-ant-xxxx
//	go run ./scripts/antigravity_relay_stream_probe -key sk-ant-xxxx -model gemini-3.5-flash-extra-low
//	go run ./scripts/antigravity_relay_stream_probe -key sk-ant-xxxx -mode nostream
//	go run ./scripts/antigravity_relay_stream_probe -key sk-ant-xxxx -base-url http://127.0.0.1:18444 -prompt "详述你的推理过程"
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

// ===== 以下结构体复刻 v1internal_api_document.md 的请求/响应字段子集 =====
// 不 import internal/relay(避免拖中继依赖),只复制直连探测用到的子集字段。

// V1InternalRequest 对应 v1internal 外层包体:project/requestId/model/request。
// 与 internal/relay/compat.go:1814 解析的 v1internalReq 同名同结构(子集)。
type V1InternalRequest struct {
	Project   string                 `json:"project"`
	RequestID string                 `json:"requestId"`
	Model     string                 `json:"model"`
	Request   GeminiGenerateRequest  `json:"request"`
}

// GeminiGenerateRequest 对应 v1internal 内层标准 Gemini 请求对象。
type GeminiGenerateRequest struct {
	Contents        []GeminiContent      `json:"contents"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiContent 对应 Gemini contents[] 元素。
type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart 对应 Gemini parts[] 元素,这里只用到 text。
type GeminiPart struct {
	Text string `json:"text"`
}

// GeminiGenerationConfig 对应 generationConfig 子集。
type GeminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

// 默认参数(可用 flag 覆盖)。
const (
	// 中继分发服务默认地址:用户指定的 172.16.10.114:18444。
	defaultBaseURL = "http://172.16.10.114:18444"
	// v1internal 文档示例模型,antigravity 网页通道经中继映射后可跑通。
	defaultModel = "gemini-3.5-flash-extra-low"
	// 给真实代码片段让它分析,逼出思考链(与 nvidia 探针同一 prompt 思路)。
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
	// 默认 project 占位:v1internal 文档示例值。中继端会自愈,留空也行,这里给个显式值便于排查。
	defaultProject = "favorable-synapse-ttvcb"
)

// modeStat 记录一次模式探测的统计结果,供跨模式汇总对照。
type modeStat struct {
	mode             string
	status          int
	err              string
	textFrames      int // text 正文 part 命中帧数
	thoughtFrames   int // thought 思考文本 part 命中帧数
	thoughtSigFrm   int // thoughtSignature 签名 part 命中帧数
	thoughtSummFrm  int // thoughtSummaryText 思考摘要 part 命中帧数
	finishReason   string
	logPath          string
}

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "中继分发服务地址(默认 http://172.16.10.114:18444)")
	key := flag.String("key", "", "中继 API Key(sk-ant- 前缀);也可用环境变量 RELAY_API_KEY")
	model := flag.String("model", defaultModel, "目标模型名(v1internal 简写,如 gemini-3.5-flash-extra-low)")
	prompt := flag.String("prompt", defaultPrompt, "测试 prompt")
	project := flag.String("project", defaultProject, "云助手项目 ID(中继端会自愈,默认 favorable-synapse-ttvcb)")
	// mode: both / stream / nostream,默认 both(流式 + 非流式各打一次对照)。
	modeStr := flag.String("mode", "both", "探测模式: both | stream | nostream")
	logDir := flag.String("log-dir", "", "日志文件输出目录(不填则写到 ./logs/)")
	flag.Parse()

	// Key 解析:-key 优先,其次环境变量 RELAY_API_KEY。
	apiKey := strings.TrimSpace(*key)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("RELAY_API_KEY"))
	}
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "[错误] 未提供 API Key。请用 -key sk-ant-xxxx 或设置环境变量 RELAY_API_KEY。\n")
		os.Exit(1)
	}

	modes := parseModes(*modeStr)
	if len(modes) == 0 {
		fmt.Fprintf(os.Stderr, "[错误] -mode 解析为空,请填 both / stream / nostream\n")
		os.Exit(1)
	}

	// ===== tee: 每模式一个独立日志文件,屏幕也实时输出 =====
	summaryPath := buildLogPath(*logDir, "summary")
	realStdout := dupStdout()
	startTee(realStdout, summaryPath)

	fmt.Printf("==== Antigravity 中继 v1internal 流式探针 ====\n")
	fmt.Printf("BaseURL  : %s\n", *baseURL)
	fmt.Printf("Model    : %s\n", *model)
	fmt.Printf("Project  : %s\n", *project)
	fmt.Printf("Modes    : %v\n", modes)
	fmt.Printf("Prompt   : %q\n", *prompt)
	fmt.Printf("Key      : %s...\n", safePrefix(apiKey, 14))
	fmt.Printf("总览日志 : %s (跨模式汇总写这里)\n", summaryPath)
	fmt.Printf("分模式日志: 每模式单独一个文件,见下方各模式输出\n\n")

	var stats []*modeStat
	for _, mode := range modes {
		fmt.Printf("######## 模式 mode=%s ########\n", mode)
		modeLog := buildLogPath(*logDir, "mode_"+mode)
		st := &modeStat{mode: mode, status: -1, logPath: modeLog}
		stats = append(stats, st)
		runOnceMode(*baseURL, apiKey, *model, *project, *prompt, mode, modeLog, st)
		fmt.Println()
	}

	// ===== 跨模式汇总 =====
	fmt.Println("==================== 响应内容跨模式汇总 ====================")
	fmt.Printf("%-10s %-7s %-8s %-9s %-14s %-17s %-9s\n",
		"Mode", "Status", "text", "thought", "thoughtSig", "thoughtSummary", "finish")
	anyHasThink := false
	for _, st := range stats {
		mark := "✗ 无思考链"
		if st.thoughtFrames > 0 || st.thoughtSigFrm > 0 || st.thoughtSummFrm > 0 {
			mark = "✓ 带思考链"
			anyHasThink = true
		}
		if st.err != "" {
			mark = "✗ 报错:" + truncate(st.err, 24)
		}
		finish := st.finishReason
		if finish == "" {
			finish = "-"
		}
		fmt.Printf("%-10s %-7d %-8d %-9d %-14d %-17d %-9s  %s\n",
			st.mode, st.status, st.textFrames, st.thoughtFrames, st.thoughtSigFrm, st.thoughtSummFrm, finish, mark)
		fmt.Printf("           日志: %s\n", st.logPath)
	}
	fmt.Println("============================================================")
	if anyHasThink {
		fmt.Println("最终结论: 中继 v1internal 在至少一种模式下用独立思考字段(thought/thoughtSignature/thoughtSummaryText)承载推理过程 → 思考链随响应回来了 ✓")
	} else {
		fmt.Println("最终结论: 各模式 delta 均未见独立思考字段 → 上游可能没开思考(模型不支持/参数未指定),请查各模式日志的响应头与错误体")
	}

	flushTee()
}

// parseModes 解析 -mode 字符串,both 展开为 [stream,nostream],去空白去重保序。
func parseModes(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "both":
		return []string{"stream", "nostream"}
	case "stream", "nostream":
		return []string{s}
	default:
		fmt.Fprintf(os.Stderr, "[错误] -mode 取值非法: %q (允许 both/stream/nostream)\n", s)
		return nil
	}
}

// runOnceMode 对一种模式发起一次 v1internal 请求,流式则逐帧打印 SSE 并统计思考字段命中。
func runOnceMode(baseURL, apiKey, model, project, prompt, mode, modeLog string, stat *modeStat) {
	stopTee := startModeTee(modeLog)
	defer stopTee()

	// 构造请求体: v1internal 外层包体。
	v1Req := &V1InternalRequest{
		Project:   project,
		RequestID: fmt.Sprintf("chat/probe-%d", timeNowUnixMilliSafe()),
		Model:     model,
		Request: GeminiGenerateRequest{
			Contents: []GeminiContent{
				{
					Role:  "user",
					Parts: []GeminiPart{{Text: prompt}},
				},
			},
			GenerationConfig: &GeminiGenerationConfig{
				MaxOutputTokens: 2048,
				Temperature:     0.2,
			},
		},
	}
	body, err := json.Marshal(v1Req)
	if err != nil {
		stat.err = "构造请求体失败: " + err.Error()
		fmt.Printf("[构造请求体失败] %v\n", err)
		return
	}

	// 目标 URL: 流式 /v1internal:streamGenerateContent?alt=sse; 非流式 /v1internal:generateContent。
	var targetURL string
	isStream := mode == "stream"
	if isStream {
		targetURL = strings.TrimRight(baseURL, "/") + "/v1internal:streamGenerateContent?alt=sse"
	} else {
		targetURL = strings.TrimRight(baseURL, "/") + "/v1internal:generateContent"
	}

	fmt.Printf("[模式] %s | 流式=%v\n", mode, isStream)
	fmt.Printf("[目标] %s\n", targetURL)
	fmt.Printf("[请求体] 完整 JSON: %s\n\n", string(body))

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		stat.err = "构造请求失败: " + err.Error()
		fmt.Printf("[构造请求失败] %v\n", err)
		return
	}
	// 请求头与 internal/relay/compat.go:603-604 对齐(User-Agent 仿网页通道)。
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// extractToken 还认 X-API-Key / ANTHROPIC_API_KEY / API_KEY,这里主用 Authorization Bearer。
	req.Header.Set("User-Agent", "antigravity/hub/2.3.1 (aidev_client; os_type=windows; arch=amd64)")
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	// 流式用无读超时,避免长输出被提前掐断;非流式给 10 分钟兜底。
	client := &http.Client{Timeout: 0}
	if !isStream {
		client.Timeout = 10 * time.Minute
	}

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
	fmt.Printf("[响应头 Transfer-Encoding] %q\n", resp.Header.Get("Transfer-Encoding"))
	fmt.Printf("[响应头 X-Accel-Buffering] %q\n", resp.Header.Get("X-Accel-Buffering"))

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		errStr := truncate(string(errBytes), 2000)
		stat.err = fmt.Sprintf("中继 %d: %s", resp.StatusCode, truncate(string(errBytes), 80))
		fmt.Printf("[中继非 200 错误体,mode=%s]\n%s\n", mode, errStr)
		return
	}

	if !isStream {
		// 非流式: 一次性读全文,打印 + 解析单包 JSON。
		data, errRead := io.ReadAll(resp.Body)
		if errRead != nil {
			stat.err = "读响应体失败: " + errRead.Error()
			fmt.Printf("[读响应体失败] %v\n", errRead)
			return
		}
		fmt.Printf("[非流式] 响应体长度 %d bytes,完整内容:\n%s\n", len(data), truncate(string(data), 4000))
		parseV1InternalChunk(data, stat, "非流式")
		return
	}

	// 流式逐行扫描: 8MB 单行缓冲,避免长帧被截断(与 nvidia 探针一致)。
	fmt.Printf("[流式] mode=%s 逐行读取 SSE, 带前缀 [行号 | ms | cumB]:\n", mode)
	flusher := bufio.NewReader(resp.Body)
	scanner := bufio.NewScanner(flusher)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	totalBytes := 0
	firstFrameAt := time.Duration(0)
	firstFrameSet := false
	dataCount := 0
	chunkCount := 0
	// part 类型命中累计:穷举 candidates[].content.parts[]. 所有 key,不预设字段名。
	partFieldHits := map[string]int{}
	var lastUsage *GeminiUsageMeta
	var thinkingTextBuf strings.Builder
	var textBuf strings.Builder

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
		if !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "data: ") {
			continue
		}
		var data string
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		} else {
			data = strings.TrimPrefix(line, "data:")
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			fmt.Printf("  ↑ 收到 [DONE],mode=%s 流结束\n", mode)
			break
		}
		dataCount++

		// 解析 v1internal SSE chunk: {"response":{"candidates":[...],"usageMetadata":{...}},"traceId":"..."}
		var chunk V1InternalStreamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			fmt.Printf("  [ warn] data 帧解析失败,原样保留: %s\n", truncate(data, 200))
			continue
		}
		chunkCount++

		// usageMetadata 命中。
		if chunk.Response.UsageMetadata != nil {
			lastUsage = chunk.Response.UsageMetadata
			fmt.Printf("  ↑ 本帧命中 usageMetadata: prompt=%d candidates=%d thoughts=%d total=%d\n",
				lastUsage.PromptTokenCount, lastUsage.CandidatesTokenCount, lastUsage.ThoughtsTokenCount, lastUsage.TotalTokenCount)
		}

		// candidates 穷举。
		for ci, cand := range chunk.Response.Candidates {
			if cand.FinishReason != "" {
				stat.finishReason = cand.FinishReason
				fmt.Printf("  ↑ candidate[%d] finishReason=%s\n", ci, cand.FinishReason)
			}
			if len(cand.Content.Parts) == 0 {
				continue
			}
			// 穷举本帧所有 part 的所有 key,统计命中。
			for pi, part := range cand.Content.Parts {
				// part.Dump 是 part 的原始 JSON 对象(map[string]json.RawMessage),穷举其 key。
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
				for _, k := range keys {
					partFieldHits[k]++
					if thinkers[k] {
						tags = append(tags, k)
					}
					// 累积文本: thought / text 都算。
					if k == "text" || k == "thought" || k == "thoughtSummaryText" {
						var s string
						if json.Unmarshal(partMap[k], &s) == nil {
							if k == "text" {
								textBuf.WriteString(s)
							} else {
								thinkingTextBuf.WriteString(s)
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
						fmt.Printf("      · %s = %s\n", k, raw)
					} else {
						fmt.Printf("      · %s = %s\n", k, truncate(raw, 200))
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("[流式扫描出错] %v\n", err)
	}

	// 回填该模式统计。
	stat.textFrames = partFieldHits["text"]
	stat.thoughtFrames = partFieldHits["thought"]
	stat.thoughtSigFrm = partFieldHits["thoughtSignature"]
	stat.thoughtSummFrm = partFieldHits["thoughtSummaryText"]

	fmt.Println("---- mode=" + mode + " 流式汇总 ----")
	fmt.Printf("首帧时延      : %v\n", firstFrameAt)
	fmt.Printf("总行数        : %d\n", lineNo)
	fmt.Printf("data: 帧数    : %d (成功解析 chunk: %d)\n", dataCount, chunkCount)
	fmt.Printf("累计字节      : %d\n", totalBytes)
	fmt.Printf("总耗时        : %v\n", time.Since(start))
	if lastUsage != nil {
		fmt.Printf("末帧 usage    : prompt=%d candidates=%d thoughts=%d total=%d\n",
			lastUsage.PromptTokenCount, lastUsage.CandidatesTokenCount, lastUsage.ThoughtsTokenCount, lastUsage.TotalTokenCount)
	} else {
		fmt.Printf("末帧 usage    : (未命中)\n")
	}
	fmt.Println("---- mode=" + mode + " 思考链标记统计 ----")
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
				mark = "(正文)"
			case "thought", "thoughtSignature", "thoughtSummaryText":
				mark = "(思考链 ✓)"
			}
			fmt.Printf("  %-20s 出现 %d 次 %s\n", h.name, h.count, mark)
		}
		hasThink := stat.thoughtFrames > 0 || stat.thoughtSigFrm > 0 || stat.thoughtSummFrm > 0
		if hasThink {
			fmt.Printf("结论: mode=%s → 上游用独立思考字段承载推理,思考链随响应回来 ✓\n", mode)
			thought := thinkingTextBuf.String()
			if thought == "" {
				fmt.Printf("思考字段命中但文本为空(可能只有 thoughtSignature 哨兵帧),需结合逐帧原文判断。\n")
			} else {
				fmt.Printf("累积思考文本(前 600 字):\n%s\n", truncate(thought, 600))
			}
		} else {
			fmt.Printf("结论: mode=%s → 无独立思考字段,思考内容可能关闭或混在正文 ✗\n", mode)
		}
	}
	if t := textBuf.String(); t != "" {
		fmt.Printf("累积正文文本(前 600 字):\n%s\n", truncate(t, 600))
	}
}

// parseV1InternalChunk 解析非流式单包响应,统计字段命中,回填 stat。
func parseV1InternalChunk(data []byte, stat *modeStat, label string) {
	var wrapper struct {
		Response GeminiResponse `json:"response"`
		TraceID  string         `json:"traceId"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		fmt.Printf("[%s] 响应体非标准 v1internal JSON,解析失败: %v\n", label, err)
		return
	}
	fmt.Printf("[%s] traceId=%s modelVersion=%s\n", label, wrapper.TraceID, wrapper.Response.ModelVersion)
	if wrapper.Response.UsageMetadata != nil {
		u := wrapper.Response.UsageMetadata
		fmt.Printf("[%s] usageMetadata: prompt=%d candidates=%d thoughts=%d total=%d\n",
			label, u.PromptTokenCount, u.CandidatesTokenCount, u.ThoughtsTokenCount, u.TotalTokenCount)
	}
	for ci, cand := range wrapper.Response.Candidates {
		if cand.FinishReason != "" {
			stat.finishReason = cand.FinishReason
			fmt.Printf("[%s] candidate[%d] finishReason=%s\n", label, ci, cand.FinishReason)
		}
		for pi, part := range cand.Content.Parts {
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
			fmt.Printf("[%s] candidate[%d].parts[%d] 字段: [%s]\n", label, ci, pi, strings.Join(keys, ", "))
			for _, k := range keys {
				raw := string(partMap[k])
				fmt.Printf("      · %s = %s\n", k, raw)
				switch k {
				case "text":
					stat.textFrames++
				case "thought":
					stat.thoughtFrames++
				case "thoughtSignature":
					stat.thoughtSigFrm++
				case "thoughtSummaryText":
					stat.thoughtSummFrm++
				}
			}
		}
	}
}

// ===== v1internal 响应结构体子集(Gemini 原生格式) =====

// V1InternalStreamChunk 对应 v1internal 流式单包 {"response":...,"traceId":...}.
type V1InternalStreamChunk struct {
	Response GeminiResponse `json:"response"`
	TraceID  string         `json:"traceId"`
}

// GeminiResponse 对应 v1internal response 字段(与文档 4.1 非流式响应结构一致)。
type GeminiResponse struct {
	Candidates    []GeminiCandidate   `json:"candidates"`
	UsageMetadata *GeminiUsageMeta    `json:"usageMetadata"`
	ModelVersion  string             `json:"modelVersion"`
	ResponseID    string             `json:"responseId"`
}

// GeminiCandidate 对应 candidates[]。
type GeminiCandidate struct {
	Content      GeminiContentBody `json:"content"`
	FinishReason string            `json:"finishReason"`
}

// GeminiContentBody 对应 candidate.content(role + parts),part 用 RawMessage 穷举 key。
type GeminiContentBody struct {
	Role  string             `json:"role"`
	Parts []GeminiPartDump   `json:"parts"`
}

// GeminiPartDump 用 RawMessage 承载 part,以便穷举所有 key(text/thought/thoughtSignature/thoughtSummaryText 等)。
type GeminiPartDump struct {
	Dump json.RawMessage `json:"-"`
}

// UnmarshalJSON 自定义解析 part: 原样保存 RawMessage 供穷举 key。
func (p *GeminiPartDump) UnmarshalJSON(data []byte) error {
	p.Dump = make([]byte, len(data))
	copy(p.Dump, data)
	return nil
}

// GeminiUsageMeta 对应 usageMetadata,含 thoughtsTokenCount(思考计费)。
type GeminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

// ===== 工具函数 =====

// truncate 截断长字符串用于打印, 复刻 nvidia.go:200-206 的 truncateBody 行为。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// safePrefix 安全打印 key 前缀,避免误把完整 key 打到日志。
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// buildLogPath 构造日志文件路径:dir 为空时落在 ./logs/,文件名 antigravity_probe_<时间>_<part>.log。
func buildLogPath(dir, part string) string {
	if dir == "" {
		dir = filepath.Join(".", "logs")
	}
	stamp := timeNowStampSafe()
	name := "antigravity_probe_" + stamp
	if part != "" {
		name += "_" + part
	}
	name += ".log"
	return filepath.Join(dir, name)
}

// timeNowStampSafe 返回 YYYYMMDD_HHMMSS 时间戳。脚本环境里 time.Now() 可用(非 workflow 脚本)。
func timeNowStampSafe() string {
	return time.Now().Format("20060102_150405")
}

// timeNowUnixMilliSafe 返回 Unix 毫秒,用于 requestId 生成。
func timeNowUnixMilliSafe() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// dupStdout 保存当前真实屏幕的 os.Stdout 引用(不关闭它),供 tee 恢复使用。
func dupStdout() *os.File {
	return os.Stdout
}

// ===== tee 实现(两套:总览 + 分模式,互不干扰) =====
//
// 设计要点:外层 main 起一个总览 tee(屏幕+总览文件);每模式在 runOnceMode 内部临时
// 再起一个分模式 tee(屏幕+该模式文件),分模式 tee flush 后把 os.Stdout 恢复回总览 pipe 写端,
// 这样跨模式继续写总览。两套用不同的包级写端句柄隔离。

var (
	// 总览 tee 写端(main 起,退出前 flush)。
	summaryWriter  *os.File
	summaryLogPath string
	summaryScreen  *os.File
)

// startTee 启动总览 tee:拦截 os.Stdout → goroutine 把读端字节 MultiWriter 写「真实屏幕 + 总览文件」。
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

// startModeTee 启动本模式实时 Tee: 拦截 os.Stdout, 实时 MultiWriter 写入「分模式日志文件」以及「切换前的总览 Pipe 写端」。
func startModeTee(logPath string) func() {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(os.Stdout, "[warn] 建分模式日志父目录失败(%s): %v\n", logPath, err)
		return func() {}
	}
	f, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stdout, "[warn] 创建分模式日志失败(%s): %v\n", logPath, err)
		return func() {}
	}
	savedOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stdout, "[warn] os.Pipe 失败,本模式日志可能无法分流: %v\n", err)
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
		fmt.Fprintf(os.Stdout, "[log] 分模式日志已写入: %s\n", logPath)
	}
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
