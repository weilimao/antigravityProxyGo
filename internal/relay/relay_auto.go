package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"antigravity-proxy/internal/settings"
)

// relay_auto.go: 模型映射中 Auto 并发竞速模型的核心调度器与虚拟响应处理器。
//
// 设计目标:
//   1. 双配置项支持: 自定义候选模型池 (CandidateModels) 与 控制台测速池 (BenchmarkModels) 动态融合;
//   2. 严格空默认: 默认不塞入任何候选模型, 未配置时友好提示 400 引导配置;
//   3. TTFT 首字抢占: 以首个有效响应/首包到达作为胜负判定, 胜者实时穿透推流;
//   4. 立即掐断败方: 胜者决出时纳秒级 Cancel 败方 Context, 中断上游网络连接, 绝不浪费算力;
//   5. 差错容忍: 单模型 4xx/5xx 或连接超时静默淘汰, 持续等待其他健康候选模型。

// ResolveCandidateModels 根据映射项配置与控制台测速池列表动态解析出最终参与竞速的模型清单。
// 遵循"严格空默认"准则: 若两者皆未配置模型, 严格返回空切片, 绝不臆造硬编码模型。
func ResolveCandidateModels(entry settings.ModelMappingEntry, benchmarkModels []string) []string {
	var candidates []string
	if entry.IsUseBenchmarkPool() {
		for _, m := range benchmarkModels {
			m = strings.TrimSpace(m)
			if m != "" {
				candidates = append(candidates, m)
			}
		}
	}
	for _, m := range entry.CandidateModels {
		m = strings.TrimSpace(m)
		if m != "" {
			candidates = append(candidates, m)
		}
	}

	// 去重并保序
	seen := make(map[string]struct{}, len(candidates))
	var result []string
	for _, m := range candidates {
		if _, exists := seen[m]; !exists {
			seen[m] = struct{}{}
			result = append(result, m)
		}
	}
	return result
}

// isAutoModel 检查入站模型名是否命中 auto 竞速模型(兜底无 Session 场景)。
func (h *APICompatHandler) isAutoModel(model string) (*settings.ModelMappingEntry, []string, bool) {
	return h.isAutoModelForSession(model, nil)
}

// isAutoModelForSession 检查入站模型名是否命中 auto 竞速模型，优先尊重当前登录中继用户的个性化私有配置。
// 返回对应映射项指针、解析后的参赛模型列表与是否命中的布尔值。
func (h *APICompatHandler) isAutoModelForSession(model string, session *RelaySession) (*settings.ModelMappingEntry, []string, bool) {
	modelTrimmed := strings.TrimSpace(model)
	if modelTrimmed == "" {
		return nil, nil, false
	}

	getBenchmarkModelsSafe := func() []string {
		if h.settingsMgr == nil {
			return nil
		}
		defer func() { _ = recover() }()
		return h.settingsMgr.GetBenchmarkConfig().Models
	}

	// 1. 若入站请求包含有效中继用户 Session，且该用户配置了专属私有 AutoConfig
	if strings.EqualFold(modelTrimmed, "auto") && session != nil && session.UserID != "" && h.authMgr != nil && h.authMgr.userMgr != nil {
		user := h.authMgr.userMgr.GetUserByID(session.UserID)
		if user != nil && user.AutoConfig != nil {
			// 若用户显式关闭了专属 auto 竞速，返回未启用
			if !user.AutoConfig.Enabled {
				entry := settings.ModelMappingEntry{
					ClientModel: "auto",
					TargetModel: "auto",
					Expose:      false,
				}
				return &entry, nil, true
			}

			useBench := user.AutoConfig.UseBenchmarkPool
			entry := settings.ModelMappingEntry{
				ClientModel:      "auto",
				TargetModel:      "auto",
				Expose:           true,
				CandidateModels:  user.AutoConfig.CandidateModels,
				UseBenchmarkPool: &useBench,
			}
			var benchmarkModels []string
			if useBench {
				benchmarkModels = getBenchmarkModelsSafe()
			}
			candidates := ResolveCandidateModels(entry, benchmarkModels)
			return &entry, candidates, true
		}
	}

	// 2. 查找全局模型映射配置
	mappings := h.getModelMapping()
	for _, entry := range mappings {
		if strings.EqualFold(strings.TrimSpace(entry.ClientModel), modelTrimmed) {
			// 若 ClientModel 本身就叫 auto, 或显式配置了候选池/开启了测速池
			if strings.EqualFold(modelTrimmed, "auto") || len(entry.CandidateModels) > 0 || entry.IsUseBenchmarkPool() {
				var benchmarkModels []string
				if entry.IsUseBenchmarkPool() {
					benchmarkModels = getBenchmarkModelsSafe()
				}
				candidates := ResolveCandidateModels(entry, benchmarkModels)
				cp := entry
				return &cp, candidates, true
			}
		}
	}

	// 3. 若映射中未显式配置但入站 model 本身就是 "auto", 兜底视为 auto 模型(候选池为空)
	if strings.EqualFold(modelTrimmed, "auto") {
		entry := settings.ModelMappingEntry{
			ClientModel: "auto",
			TargetModel: "auto",
			Expose:      true,
		}
		return &entry, nil, true
	}

	return nil, nil, false
}

// autoRaceCoordinator 是并发竞速调度的仲裁者。
type autoRaceCoordinator struct {
	mu            sync.Mutex
	realW         http.ResponseWriter
	realFlusher   http.Flusher
	winnerIdx     int
	winnerModel   string
	cancels       map[int]context.CancelFunc
	totalBranches int
	failedCount   int
	lastErrCode   int
	lastErrBody   []byte
	lastErrHeader http.Header
	logFn         func(format string, v ...interface{})
}

func newAutoRaceCoordinator(w http.ResponseWriter, total int, logFn func(format string, v ...interface{})) *autoRaceCoordinator {
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}
	return &autoRaceCoordinator{
		realW:         w,
		realFlusher:   flusher,
		winnerIdx:     -1,
		cancels:       make(map[int]context.CancelFunc, total),
		totalBranches: total,
		logFn:         logFn,
	}
}

// claimVictory 由分支尝试抢占胜者地位。只有第一个成功获得 2xx 且吐出首包数据的分支能胜出。
func (c *autoRaceCoordinator) claimVictory(idx int, model string, code int, headers http.Header, firstChunk []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.winnerIdx != -1 {
		return false
	}
	c.winnerIdx = idx
	c.winnerModel = model

	if c.logFn != nil {
		c.logFn("🏆 [Auto竞速胜出] 模型 %q (分支 %d) 率先响应 (HTTP %d, 首包 %d 字节), 胜出并接管流式输出", model, idx, code, len(firstChunk))
	}

	// 写出 HTTP 响应头 (严格清洗 Hop-by-hop 和 Content-Length 等可能导致断流或长度冲突的头部)
	for k, vv := range headers {
		if strings.EqualFold(k, "Content-Length") ||
			strings.EqualFold(k, "Transfer-Encoding") ||
			strings.EqualFold(k, "Connection") ||
			strings.EqualFold(k, "Trailer") {
			continue
		}
		for _, v := range vv {
			c.realW.Header().Add(k, v)
		}
	}
	if code <= 0 {
		code = http.StatusOK
	}
	c.realW.WriteHeader(code)

	if len(firstChunk) > 0 {
		_, _ = c.realW.Write(firstChunk)
	}
	if c.realFlusher != nil {
		c.realFlusher.Flush()
	}

	// 立即掐断所有败方分支的请求, 杜绝上游继续计算与计费
	for otherIdx, cancel := range c.cancels {
		if otherIdx != idx && cancel != nil {
			cancel()
		}
	}
	return true
}

// recordFailure 记录单个候选分支的失败或退出。
func (c *autoRaceCoordinator) recordFailure(idx int, model string, code int, body []byte, headers http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 若已有胜者出线，其余分支退出属于胜者锁定后的主动掐断，严格禁止输出 502/报错日志，避免误导用户
	if c.winnerIdx != -1 {
		if c.logFn != nil {
			c.logFn("⏹️ [Auto竞速败方中止] 模型 %q (分支 %d) 已随胜者出线主动掐断", model, idx)
		}
		return
	}

	c.failedCount++
	if c.logFn != nil {
		c.logFn("⚠️ [Auto竞速分支淘汰] 模型 %q (分支 %d) 请求失败: HTTP %d, 已淘汰 (剩余存活分支: %d/%d)", model, idx, code, c.totalBranches-c.failedCount, c.totalBranches)
	}

	// 记录最新的错误以便全灭时写回
	if code > 0 {
		c.lastErrCode = code
	}
	if len(body) > 0 {
		c.lastErrBody = body
	}
	if headers != nil {
		c.lastErrHeader = headers
	}
}

// writeFailureIfNeeded 在所有分支均失败且无胜者时输出错误响应。
func (c *autoRaceCoordinator) writeFailureIfNeeded() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.winnerIdx != -1 {
		return
	}

	statusCode := c.lastErrCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}

	if c.lastErrHeader != nil {
		for k, vv := range c.lastErrHeader {
			// 跳过可能导致格式冲突的 content-length
			if strings.EqualFold(k, "Content-Length") {
				continue
			}
			for _, v := range vv {
				c.realW.Header().Add(k, v)
			}
		}
	}
	if c.realW.Header().Get("Content-Type") == "" {
		c.realW.Header().Set("Content-Type", "application/json")
	}
	c.realW.WriteHeader(statusCode)

	if len(c.lastErrBody) > 0 {
		_, _ = c.realW.Write(c.lastErrBody)
	} else {
		errMsg := map[string]interface{}{
			"error": map[string]interface{}{
				"message": fmt.Sprintf("all candidate models in auto race failed (total %d candidates)", c.totalBranches),
				"type":    "auto_race_error",
				"code":    "all_candidates_failed",
			},
		}
		b, _ := json.Marshal(errMsg)
		_, _ = c.realW.Write(b)
	}
	if c.realFlusher != nil {
		c.realFlusher.Flush()
	}
}

// raceResponseWriter 实现了 http.ResponseWriter 与 http.Flusher, 用于分支隔离与抢占拦截。
type raceResponseWriter struct {
	coord          *autoRaceCoordinator
	branchIdx      int
	candidateModel string
	header         http.Header
	statusCode     int
	isWinner       bool
	errBuf         bytes.Buffer
}

func newRaceResponseWriter(coord *autoRaceCoordinator, idx int, model string) *raceResponseWriter {
	return &raceResponseWriter{
		coord:          coord,
		branchIdx:      idx,
		candidateModel: model,
		header:         make(http.Header),
	}
}

func (rw *raceResponseWriter) Header() http.Header {
	return rw.header
}

func (rw *raceResponseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
}

func (rw *raceResponseWriter) Write(p []byte) (int, error) {
	// 1. 若本分支已被确认为胜者, 无锁直接穿透写入真实客户端
	if rw.isWinner {
		n, err := rw.coord.realW.Write(p)
		if rw.coord.realFlusher != nil {
			rw.coord.realFlusher.Flush()
		}
		return n, err
	}

	// 2. 若上游返回了错误状态码 (>= 400), 缓冲错误 body, 不争夺胜利
	if rw.statusCode >= 400 {
		rw.errBuf.Write(p)
		return len(p), nil
	}

	// 3. 正常响应, 且为首个数据块, 尝试原子抢占胜者
	code := rw.statusCode
	if code == 0 {
		code = http.StatusOK
		rw.statusCode = code
	}

	won := rw.coord.claimVictory(rw.branchIdx, rw.candidateModel, code, rw.header, p)
	if won {
		rw.isWinner = true
		return len(p), nil
	}

	// 未抢到胜者 (已有其他分支抢先), 丢弃当前数据
	return len(p), nil
}

func (rw *raceResponseWriter) Flush() {
	if rw.isWinner && rw.coord.realFlusher != nil {
		rw.coord.realFlusher.Flush()
	}
}

func (rw *raceResponseWriter) Finish() {
	if rw.isWinner {
		return
	}
	// 如果到结束仍未成为胜者, 且状态码为 2xx 但无 body, 也尝试做最后一次胜者裁决
	if rw.statusCode >= 200 && rw.statusCode < 300 {
		won := rw.coord.claimVictory(rw.branchIdx, rw.candidateModel, rw.statusCode, rw.header, nil)
		if won {
			rw.isWinner = true
			return
		}
	}
	// 否则记为失败或淘汰
	code := rw.statusCode
	if code == 0 {
		code = http.StatusBadGateway
	}
	rw.coord.recordFailure(rw.branchIdx, rw.candidateModel, code, rw.errBuf.Bytes(), rw.header)
}

// handleAutoRace 是处理 Auto 竞速请求的主入口。
func (h *APICompatHandler) handleAutoRace(
	w http.ResponseWriter,
	r *http.Request,
	userSession *RelaySession,
	inModel string,
	bodyBytes []byte,
	isStreaming bool,
	isChat bool,
	isResponses bool,
	isMessages bool,
	candidates []string,
) {
	// 1. 空候选池防御: 严格空默认, 绝不私自塞入默认模型, 提示用户配置
	if len(candidates) == 0 {
		h.log("🚫 [Auto竞速] 模型 %q 未配置任何候选模型池, 且未关联测速池", inModel)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]interface{}{
				"message": fmt.Sprintf("auto model %q has no candidate models configured: please configure candidate models or enable benchmark pool in relay settings", inModel),
				"type":    "invalid_request_error",
				"code":    "no_candidates_configured",
			},
		})
		return
	}

	// 2. 单候选模型退化: 只有一个候选模型时, 直接转发该模型, 避免多余的 goroutine 协调开销
	if len(candidates) == 1 {
		targetModel := candidates[0]
		h.log("ℹ️ [Auto竞速] 模型 %q 仅配置 1 个候选模型 %q, 直接单路转发", inModel, targetModel)
		newBody := patchRoutedBodyModelAndStream(bodyBytes, targetModel, isStreaming)
		r.Body = io.NopCloser(strings.NewReader(newBody))
		r.ContentLength = int64(len(newBody))
		h.executeForwardModel(w, r, userSession, targetModel, []byte(newBody), isStreaming, isChat, isResponses, isMessages)
		return
	}

	// 3. 多模型并发竞速
	h.log("🚀 [Auto竞速开始] 模型 %q 并发发起 %d 个候选模型竞速: %v | stream=%v | 用户: %s",
		inModel, len(candidates), candidates, isStreaming, userSession.UserKey)

	coord := newAutoRaceCoordinator(w, len(candidates), h.log)

	var wg sync.WaitGroup
	for idx, cand := range candidates {
		candModel := cand
		branchIdx := idx

		branchCtx, cancel := context.WithCancel(r.Context())
		coord.cancels[branchIdx] = cancel

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()

			branchReq := r.Clone(branchCtx)
			patchedBody := patchRoutedBodyModelAndStream(bodyBytes, candModel, isStreaming)
			branchReq.Body = io.NopCloser(strings.NewReader(patchedBody))
			branchReq.ContentLength = int64(len(patchedBody))

			rw := newRaceResponseWriter(coord, branchIdx, candModel)
			h.executeForwardModel(rw, branchReq, userSession, candModel, []byte(patchedBody), isStreaming, isChat, isResponses, isMessages)
			rw.Finish()
		}()
	}

	// 等待胜者流式输出结束(或全部分支淘汰)
	wg.Wait()

	// 若所有分支均未胜出, 写回聚合失败错误
	coord.writeFailureIfNeeded()
}
