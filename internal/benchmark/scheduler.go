package benchmark

// benchmark 包: 模型响应测速调度器。
//
// 定时(或手动)向用户配置的模型列表发送最小流式 OpenAI Chat Completions 请求,
// 经「中继回环」(127.0.0.1 专用 http.Server 包裹 relay.APICompatHandler)复用全部
// 路由/转译/号池选号链路, 得真实端到端延迟; 测首字响应(TTFT)与总耗时(total),
// 落 SQLite(benchmark_results, 每模型一行 + 上一轮值供趋势对比), 并经 benchmark-updated
// 事件推前端仪表盘卡片。
//
// 测速请求带 X-Antigravity-Benchmark: 1 头, relay 据此置 RelaySession.IsBenchmark,
// 各 record*Usage 统计落库早退, 不污染仪表盘的请求/成功率/Token 统计。
// 鉴权走 sk-ant- 官方前缀兜底分支(映射 default_local_admin), 无需用户配 API Key。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/settings"
)

// internalToken 是测速回环请求的鉴权 Token。
// relay.AuthManager.ValidateToken 对 sk-ant- 前缀走官方兜底分支, 映射到 default_local_admin,
// 故测速无需用户额外配置 API Key; 同时该请求带 X-Antigravity-Benchmark 头跳过统计落库。
const internalToken = "sk-ant-benchmark-internal-probe-0000000000000000000000000000"

// Result 是单模型一次测速结果(与 db.BenchmarkResult 对齐, 供落库与前端展示)。
type Result = db.BenchmarkResult

// Scheduler 定时测速调度器。
type Scheduler struct {
	settings settings.ManagerInterface
	handler  http.Handler // relay.APICompatHandler(实现 http.Handler)
	addLog   func(string)
	emit     func(name string, payload any)

	listener net.Listener
	server   *http.Server
	baseURL  string
	client   *http.Client // 回环请求专用: 不走任何代理(Proxy=nil), 避免本地代理拦截 127.0.0.1

	ticker *time.Ticker
	quit   chan struct{}
	wg     sync.WaitGroup

	runningMu sync.Mutex
	running   bool
	lastRun   time.Time

	testingMu     sync.Mutex
	pendingModels map[string]bool
}

// NewScheduler 构造测速调度器。handler 须为 relay.APICompatHandler(已装配好号池/settings)。
// emit 用于向前端派发 benchmark-updated 事件(通常传 a.emitEvent)。
func NewScheduler(s settings.ManagerInterface, handler http.Handler, addLog func(string), emit func(string, any)) *Scheduler {
	return &Scheduler{
		settings: s,
		handler:  handler,
		addLog:   addLog,
		emit:     emit,
		client:   &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 0},
	}
}

// Start 启动回环监听 + 15s 节拍轮询协程。
// 15s 节拍仅用于「到点检查是否该跑」; 实际触发间隔由配置的 IntervalMinutes 决定,
// 故改配置无需重启调度器。回环监听仅在 127.0.0.1:0, 不暴露外部。
func (s *Scheduler) Start() {
	if s.handler == nil {
		if s.addLog != nil {
			s.addLog("⚠️ [测速] relay 兼容处理器未注入, 测速调度器未启动")
		}
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if s.addLog != nil {
			s.addLog(fmt.Sprintf("⚠️ [测速] 启动回环监听失败: %v", err))
		}
		return
	}
	s.listener = ln
	s.baseURL = "http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	s.server = &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	go s.server.Serve(ln)

	s.ticker = time.NewTicker(15 * time.Second)
	s.quit = make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if s.addLog != nil {
			s.addLog("⚡ [测速] 模型响应测速调度器已启动(回环 " + s.baseURL + ")")
		}
		for {
			select {
			case <-s.ticker.C:
				s.maybeRun()
			case <-s.quit:
				return
			}
		}
	}()
}

// Stop 关停回环监听与轮询协程。
func (s *Scheduler) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.quit != nil {
		close(s.quit)
	}
	s.wg.Wait()
	if s.server != nil {
		_ = s.server.Close()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.addLog != nil {
		s.addLog("⚡ [测速] 模型响应测速调度器已关闭")
	}
}

// RunNow 手动触发一次测速(异步, 立即返回; 完成后经 benchmark-updated 推送结果)。
// 供 IPC benchmark:run-now 调用。重叠运行守卫: 正在跑则跳过本次。
func (s *Scheduler) RunNow() {
	go s.runOnce()
}

// RunModelNow 手动重测单个模型(异步)。供 IPC benchmark:run-model 调用。
// 不走全局 running 守卫, 允许与整批 runOnce 并发(单模型探测互不阻塞, 各自独立超时与落库)。
func (s *Scheduler) RunModelNow(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	go s.runModelOnce(model)
}

// runModelOnce 探测单个模型并落库 + 推事件。与 runOnce 共用 probeModel, 仅范围不同。
func (s *Scheduler) runModelOnce(model string) {
	defer func() {
		s.testingMu.Lock()
		delete(s.pendingModels, model)
		s.testingMu.Unlock()
		if r := recover(); r != nil {
			if s.addLog != nil {
				s.addLog(fmt.Sprintf("⚠️ [测速] 单模型重测异常 %s: %v", model, r))
			}
		}
	}()

	s.testingMu.Lock()
	if s.pendingModels == nil {
		s.pendingModels = make(map[string]bool)
	}
	s.pendingModels[model] = true
	s.testingMu.Unlock()
	s.EmitResults()

	cfg := s.settings.GetBenchmarkConfig()
	prompt := cfg.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = "Hi"
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout < 5*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r := s.probeModel(ctx, model, prompt)
	if r.TestedAt == "" {
		r.TestedAt = time.Now().Format(time.RFC3339)
	}
	_ = db.UpsertBenchmarkResult(&r)

	s.testingMu.Lock()
	delete(s.pendingModels, model)
	s.testingMu.Unlock()

	s.runningMu.Lock()
	s.lastRun = time.Now()
	s.runningMu.Unlock()
	s.EmitResults()
}

// IsRunning 返回是否正在执行一轮测速(供 IPC benchmark:get 展示 loading 态)。
func (s *Scheduler) IsRunning() bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.running
}

// LastRun 返回上次测速完成时刻。
func (s *Scheduler) LastRun() time.Time {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.lastRun
}

// ResetLastRun 清零上次测速时刻, 使配置变更后下一节拍立即触发(而非等满旧间隔)。
func (s *Scheduler) ResetLastRun() {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.lastRun = time.Time{}
}

// PendingModels 返回当前正在等待或测试中的模型列表。
func (s *Scheduler) PendingModels() []string {
	s.testingMu.Lock()
	defer s.testingMu.Unlock()
	out := make([]string, 0, len(s.pendingModels))
	for m := range s.pendingModels {
		out = append(out, m)
	}
	return out
}

// maybeRun 节拍回调: 启用且到点则跑一轮。
func (s *Scheduler) maybeRun() {
	if s.settings == nil {
		return
	}
	cfg := s.settings.GetBenchmarkConfig()
	if !cfg.Enabled || len(cfg.Models) == 0 {
		return
	}
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	s.runningMu.Lock()
	last := s.lastRun
	running := s.running
	s.runningMu.Unlock()
	if running {
		return
	}
	if !last.IsZero() && time.Since(last) < interval {
		return
	}
	s.runOnce()
}

// runOnce 执行一轮测速: 并发(上限 4)探测全部配置模型, 落库, 推事件。
func (s *Scheduler) runOnce() {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		return
	}
	s.running = true
	s.runningMu.Unlock()

	// 立即广播 running=true, 使前端卡片立即进入测速中(全部模型测试图标旋转)
	s.EmitResults()

	// defer 兜底置 running=false(防早返/异常路径遗漏); 正常路径在 emit 前已显式置 false。
	defer func() {
		s.runningMu.Lock()
		s.running = false
		s.runningMu.Unlock()
		s.testingMu.Lock()
		s.pendingModels = nil
		s.testingMu.Unlock()
		if r := recover(); r != nil {
			if s.addLog != nil {
				s.addLog(fmt.Sprintf("⚠️ [测速] 运行异常: %v", r))
			}
		}
	}()

	cfg := s.settings.GetBenchmarkConfig()
	if len(cfg.Models) == 0 {
		s.EmitResults()
		return
	}

	// 初始化当前轮次待测模型集合
	s.testingMu.Lock()
	s.pendingModels = make(map[string]bool, len(cfg.Models))
	for _, m := range cfg.Models {
		s.pendingModels[m] = true
	}
	s.testingMu.Unlock()

	// 立即广播 running=true 与 pendingModels, 使前端卡片立即进入测速中(全部待测模型测试图标旋转)
	s.EmitResults()

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout < 5*time.Second {
		timeout = 30 * time.Second
	}
	prompt := cfg.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = "Hi"
	}

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, model := range cfg.Models {
		wg.Add(1)
		sem <- struct{}{}
		go func(m string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			res := s.probeModel(ctx, m, prompt)
			if res.TestedAt == "" {
				res.TestedAt = time.Now().Format(time.RFC3339)
			}
			// 单个模型测试完成(成功或失败均已出结果): 立即落库!
			_ = db.UpsertBenchmarkResult(&res)
			// 从当前待测集合中移除该模型
			s.testingMu.Lock()
			delete(s.pendingModels, m)
			s.testingMu.Unlock()
			// 一个完成立即推送显示, 不需要等全部测完才显示
			s.EmitResults()
		}(model)
	}
	wg.Wait()

	s.runningMu.Lock()
	s.lastRun = time.Now()
	s.running = false
	s.runningMu.Unlock()
	s.testingMu.Lock()
	s.pendingModels = nil
	s.testingMu.Unlock()
	s.EmitResults()
}

// probeModel 对单模型发一次流式 OpenAI Chat Completions 请求并测 TTFT/总耗时。
// 走中继回环(127.0.0.1) → relay.APICompatHandler → 完整路由/转译/选号/上游流式,
// 故测得的是「客户端感知的端到端延迟」, 与真实客户端请求口径一致。
func (s *Scheduler) probeModel(ctx context.Context, model, prompt string) Result {
	now := time.Now()
	res := Result{Model: model, Status: "error", TestedAt: now.Format(time.RFC3339)}

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"stream":     true,
		"max_tokens": 16,
	})
	if err != nil {
		res.Error = truncateErr(err.Error())
		return res
	}

	// 按模型所属号池选入口, 使测速与真实客户端(18444 入站)同口径:
	// 非 Google 族模型(nvidia/grok/other 等)必须走 /route(handleRoutedForward → 对应号池),
	// 否则 nvidia/moonshotai/kimi-k3 这类模型会被当 Gemini 请求丢进 18443 的 antigravity
	// 网页号池, 上游 Google 对不存在的模型路径回 404, 测速恒败。Google 族保持原入口。
	endpoint := "/v1/chat/completions"
	if resolver, ok := s.handler.(interface{ ResolveBenchmarkEntryPath(string) string }); ok {
		endpoint = resolver.ResolveBenchmarkEntryPath(model)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+endpoint, bytes.NewReader(reqBody))
	if err != nil {
		res.Error = truncateErr(err.Error())
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+internalToken)
	req.Header.Set("Accept", "text/event-stream")
	// 测速标记: relay 据此置 RelaySession.IsBenchmark, 跳过 stats 落库。
	req.Header.Set("X-Antigravity-Benchmark", "1")

	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		res.Error = truncateErr(err.Error())
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		res.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateErr(string(b)))
		return res
	}

	// 扫 SSE 流: 首个非空 data: 行 = 首字帧(TTFT); data: [DONE] / EOF = 流结束(total)。
	// 与 relay 自身 FirstByteRecorder 的「首个非空 SSE 行」口径一致。
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var firstByte time.Time
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		if data == "" {
			continue
		}
		if firstByte.IsZero() {
			firstByte = time.Now()
		}
	}

	end := time.Now()
	ttftMs := int64(0)
	if !firstByte.IsZero() {
		ttftMs = firstByte.Sub(start).Milliseconds()
	}
	if ttftMs <= 0 && !firstByte.IsZero() {
		ttftMs = 1
	}

	// 耗时: 依用户需求只统计「首帧到结束的时间」(即模型流式输出阶段耗时)
	// 若未采集到首帧(如非流式或直接报错), 则兜底统计端到端总时间
	totalMs := int64(0)
	if !firstByte.IsZero() {
		totalMs = end.Sub(firstByte).Milliseconds()
	} else {
		totalMs = end.Sub(start).Milliseconds()
		ttftMs = totalMs
	}
	if totalMs <= 0 {
		totalMs = 1
	}

	status := "ok"
	if ttftMs > 2000 {
		status = "warning"
	}
	return Result{
		Model:    model,
		TTFTMs:   ttftMs,
		TotalMs:  totalMs,
		Status:   status,
		TestedAt: start.Format(time.RFC3339),
	}
}

// EmitResults 读最新结果 + 配置, 经 benchmark-updated 事件推前端。
// running 已在调用前置 false, 故 payload.running 反映真实空闲态。
// 导出供 App 在配置清空模型时同步推送空态。
func (s *Scheduler) EmitResults() {
	results, _ := db.ListBenchmarkResults()
	cfg := settings.BenchmarkConfig{}
	if s.settings != nil {
		cfg = s.settings.GetBenchmarkConfig()
	}

	s.testingMu.Lock()
	pendingList := make([]string, 0, len(s.pendingModels))
	for m := range s.pendingModels {
		pendingList = append(pendingList, m)
	}
	s.testingMu.Unlock()

	payload := map[string]interface{}{
		"config": map[string]interface{}{
			"enabled":         cfg.Enabled,
			"models":          cfg.Models,
			"intervalMinutes": cfg.IntervalMinutes,
			"prompt":          cfg.Prompt,
			"timeoutMs":       cfg.TimeoutMs,
		},
		"results":       results,
		"pendingModels": pendingList,
		"lastRun":       s.LastRun().Format(time.RFC3339),
		"running":       s.IsRunning(),
	}
	if s.emit != nil {
		s.emit("benchmark-updated", payload)
	}
}

// truncateErr 裁剪错误文本到 200 字符内并去掉换行, 供卡片展示与 DB 落库。
func truncateErr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:197] + "..."
	}
	return s
}
