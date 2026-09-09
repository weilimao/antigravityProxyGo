package relay

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/settings"
)

// TestResolveCandidateModels_DualPool 测试双配置项(自定义池与测速池)的解析逻辑与严格空默认
func TestResolveCandidateModels_DualPool(t *testing.T) {
	// 1. 严格空默认: 两个配置项均未配置或为空
	emptyEntry := settings.ModelMappingEntry{
		ClientModel: "auto",
		TargetModel: "auto",
	}
	res1 := ResolveCandidateModels(emptyEntry, nil)
	if len(res1) != 0 {
		t.Fatalf("严格空默认下候选池必须为空, 实际得到: %v", res1)
	}

	// 2. 仅自定义模型池
	customEntry := settings.ModelMappingEntry{
		ClientModel:     "auto",
		CandidateModels: []string{"gemini-2.5-flash", "deepseek-chat", "gemini-2.5-flash"},
	}
	res2 := ResolveCandidateModels(customEntry, nil)
	if len(res2) != 2 || res2[0] != "gemini-2.5-flash" || res2[1] != "deepseek-chat" {
		t.Fatalf("自定义模型池解析去重失败: %v", res2)
	}

	// 3. 仅控制台测速池
	useBench := true
	benchEntry := settings.ModelMappingEntry{
		ClientModel:      "auto",
		UseBenchmarkPool: &useBench,
	}
	benchModels := []string{"nvidia/llama-3.3-70b", "other/openai/gpt-4o"}
	res3 := ResolveCandidateModels(benchEntry, benchModels)
	if len(res3) != 2 || res3[0] != "nvidia/llama-3.3-70b" || res3[1] != "other/openai/gpt-4o" {
		t.Fatalf("测速池模型解析失败: %v", res3)
	}

	// 4. 双配置项同时启用: 合并去重
	mergedEntry := settings.ModelMappingEntry{
		ClientModel:      "auto",
		CandidateModels:  []string{"gemini-2.5-flash", "nvidia/llama-3.3-70b"},
		UseBenchmarkPool: &useBench,
	}
	res4 := ResolveCandidateModels(mergedEntry, benchModels)
	// 期望: nvidia/llama-3.3-70b (来自测速池), other/openai/gpt-4o (来自测速池), gemini-2.5-flash (来自自定义)
	if len(res4) != 3 {
		t.Fatalf("双配置项合并去重失败, 期望 3 项, 实际得到 %d: %v", len(res4), res4)
	}
}

// TestAutoRace_EmptyCandidates_Returns400 校验未配置候选模型时友好拦截并返回 400
func TestAutoRace_EmptyCandidates_Returns400(t *testing.T) {
	h := &APICompatHandler{}
	rec := httptest.NewRecorder()
	reqBody := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/route/v1/chat/completions", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	sess := &RelaySession{UserID: "test_user", UserKey: "key_test"}

	h.handleAutoRace(rec, req, sess, "auto", reqBody, false, true, false, false, []string{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("期望 HTTP 400, 实际为 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no_candidates_configured") {
		t.Fatalf("期望包含 no_candidates_configured 错误码, 实际内容: %s", rec.Body.String())
	}
}

// TestAutoRace_FasterModelWins 验证并发竞速中响应更快的模型胜出, 且慢模型派生 Context 立即被 Cancel
func TestAutoRace_FasterModelWins(t *testing.T) {
	var modelACanceled int32
	var modelBFinished int32

	// mock 上游分发: 通过自定义 APICompatHandler 或 coordinator 验证
	coord := newAutoRaceCoordinator(httptest.NewRecorder(), 2, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctxA, cancelA := context.WithCancel(ctx)
	ctxB, cancelB := context.WithCancel(ctx)
	coord.cancels[0] = cancelA
	coord.cancels[1] = cancelB

	// 分支 0: 慢模型 (模拟 150ms 后返回)
	go func() {
		defer cancelA()
		select {
		case <-time.After(150 * time.Millisecond):
			rw := newRaceResponseWriter(coord, 0, "model-slow")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(`{"content":"from slow"}`))
			rw.Finish()
		case <-ctxA.Done():
			atomic.StoreInt32(&modelACanceled, 1)
		}
	}()

	// 分支 1: 快模型 (模拟 20ms 后返回)
	go func() {
		defer cancelB()
		select {
		case <-time.After(20 * time.Millisecond):
			rw := newRaceResponseWriter(coord, 1, "model-fast")
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(`{"content":"from fast"}`))
			rw.Finish()
			atomic.StoreInt32(&modelBFinished, 1)
		case <-ctxB.Done():
		}
	}()

	// 等待裁决
	time.Sleep(80 * time.Millisecond)

	coord.mu.Lock()
	winner := coord.winnerModel
	winnerIdx := coord.winnerIdx
	coord.mu.Unlock()

	if winner != "model-fast" || winnerIdx != 1 {
		t.Fatalf("期望 model-fast (分支 1) 胜出, 实际胜者: %s (idx: %d)", winner, winnerIdx)
	}

	// 校验慢模型的 Context 是否被立即掐断
	time.Sleep(30 * time.Millisecond)
	if atomic.LoadInt32(&modelACanceled) != 1 {
		t.Fatal("胜者产生后, 慢模型分支 Context 必须被立即 Cancel")
	}
}

// TestAutoRace_FaultTolerance 验证快速报错的分支被淘汰, 稍慢但成功的分支最终胜出
func TestAutoRace_FaultTolerance(t *testing.T) {
	rec := httptest.NewRecorder()
	coord := newAutoRaceCoordinator(rec, 2, nil)

	// 分支 0: 快速返回 500
	rw0 := newRaceResponseWriter(coord, 0, "model-error")
	rw0.WriteHeader(http.StatusInternalServerError)
	_, _ = rw0.Write([]byte(`{"error":"upstream crash"}`))
	rw0.Finish()

	// 校验此时不应有胜者
	coord.mu.Lock()
	winner0 := coord.winnerIdx
	coord.mu.Unlock()
	if winner0 != -1 {
		t.Fatal("500 错误分支不应胜出")
	}

	// 分支 1: 随后成功返回 200
	rw1 := newRaceResponseWriter(coord, 1, "model-healthy")
	rw1.WriteHeader(http.StatusOK)
	_, _ = rw1.Write([]byte(`{"choices":[{"message":{"content":"healthy response"}}]}`))
	rw1.Finish()

	coord.mu.Lock()
	winner1 := coord.winnerModel
	coord.mu.Unlock()

	if winner1 != "model-healthy" {
		t.Fatalf("期望健康分支胜出, 实际为: %s", winner1)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("真实 ResponseWriter 期望 HTTP 200, 实际为: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "healthy response") {
		t.Fatalf("真实响应体应为健康分支内容: %s", rec.Body.String())
	}
}

// TestAutoRace_AllCandidatesFailed 验证所有分支均失败时输出聚合错误
func TestAutoRace_AllCandidatesFailed(t *testing.T) {
	rec := httptest.NewRecorder()
	coord := newAutoRaceCoordinator(rec, 2, nil)

	rw0 := newRaceResponseWriter(coord, 0, "model-1")
	rw0.WriteHeader(http.StatusTooManyRequests)
	_, _ = rw0.Write([]byte(`{"error":"rate limited"}`))
	rw0.Finish()

	rw1 := newRaceResponseWriter(coord, 1, "model-2")
	rw1.WriteHeader(http.StatusBadGateway)
	_, _ = rw1.Write([]byte(`{"error":"gateway down"}`))
	rw1.Finish()

	coord.writeFailureIfNeeded()

	if rec.Code != http.StatusBadGateway && rec.Code != http.StatusTooManyRequests {
		t.Fatalf("全灭状态下应输出失败状态码, 实际为 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gateway down") && !strings.Contains(rec.Body.String(), "all candidate models") {
		t.Fatalf("输出体应包含错误描述: %s", rec.Body.String())
	}
}

// TestAutoRace_StreamingSSE_PassThrough 验证流式 SSE 下首包抢占后, 后续 chunk 能够完整穿透输出
func TestAutoRace_StreamingSSE_PassThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	coord := newAutoRaceCoordinator(rec, 1, nil)

	rw := newRaceResponseWriter(coord, 0, "model-stream")
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.WriteHeader(http.StatusOK)

	// 写入首帧 (抢占胜者)
	_, err := rw.Write([]byte("data: {\"token\":\"hello\"}\n\n"))
	if err != nil {
		t.Fatalf("write chunk 1 failed: %v", err)
	}
	// 写入后续帧
	_, err = rw.Write([]byte("data: {\"token\":\" world\"}\n\n"))
	if err != nil {
		t.Fatalf("write chunk 2 failed: %v", err)
	}
	_, err = rw.Write([]byte("data: [DONE]\n\n"))
	if err != nil {
		t.Fatalf("write chunk 3 failed: %v", err)
	}
	rw.Finish()

	body := rec.Body.String()
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("流式数据穿透不完整: %s", body)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type 应为 text/event-stream, 实际为 %s", rec.Header().Get("Content-Type"))
	}
}

// TestAutoRace_E2E_Dispatch 验证从 OpenAI 与 Anthropic 端点入站时 auto 模型正确被识别并执行分发
func TestAutoRace_E2E_Dispatch(t *testing.T) {
	h := &APICompatHandler{}

	// 1. isAutoModel 对未显式配置的 "auto" 字符串
	entry, cands, ok := h.isAutoModel("auto")
	if !ok {
		t.Fatal("模型名 auto 必须被识别为 auto 竞速模型")
	}
	if entry.ClientModel != "auto" {
		t.Fatalf("entry ClientModel 期望 auto, 实际为 %s", entry.ClientModel)
	}
	if len(cands) != 0 {
		t.Fatalf("未配置候选池时, candidates 必须严格为空, 实际为 %v", cands)
	}

	// 2. 空候选池调用 OpenAI 端点拦截验证
	recOpenAI := httptest.NewRecorder()
	reqBodyOpenAI := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	reqOpenAI := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(reqBodyOpenAI))
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	h.handleOpenAIChat(recOpenAI, reqOpenAI, sess)
	if recOpenAI.Code != http.StatusBadRequest {
		t.Fatalf("空池调用期望 400, 实际为 %d", recOpenAI.Code)
	}
	if !strings.Contains(recOpenAI.Body.String(), "no_candidates_configured") {
		t.Fatalf("应包含 no_candidates_configured 提示, 实际输出: %s", recOpenAI.Body.String())
	}

	// 3. 空候选池调用 Anthropic 端点拦截验证
	recAnth := httptest.NewRecorder()
	reqBodyAnth := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}],"max_tokens":1024}`)
	reqAnth := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBodyAnth))

	h.handleAnthropicMessages(recAnth, reqAnth, sess)
	if recAnth.Code != http.StatusBadRequest {
		t.Fatalf("空池调用 Anthropic 期望 400, 实际为 %d", recAnth.Code)
	}
	if !strings.Contains(recAnth.Body.String(), "no_candidates_configured") {
		t.Fatalf("应包含 no_candidates_configured 提示, 实际输出: %s", recAnth.Body.String())
	}
}

// TestAutoRace_LoserBranch_No502Logged 验证胜者出线后，败方分支因被掐断而退出时，不会打印误导性的 502/报错日志
func TestAutoRace_LoserBranch_No502Logged(t *testing.T) {
	var loggedMessages []string
	var mu sync.Mutex
	logFn := func(format string, args ...interface{}) {
		mu.Lock()
		defer mu.Unlock()
		loggedMessages = append(loggedMessages, fmt.Sprintf(format, args...))
	}

	rec := httptest.NewRecorder()
	coord := newAutoRaceCoordinator(rec, 2, logFn)

	// 分支 0: 胜者率先响应 200
	rw0 := newRaceResponseWriter(coord, 0, "fast-winner")
	rw0.WriteHeader(http.StatusOK)
	_, _ = rw0.Write([]byte(`{"result":"ok"}`))
	rw0.Finish()

	// 分支 1: 败方在胜者产生后退出 (模拟底层因 context canceled 抛出 502)
	rw1 := newRaceResponseWriter(coord, 1, "slow-loser")
	rw1.WriteHeader(http.StatusBadGateway)
	_, _ = rw1.Write([]byte(`{"error":"upstream context canceled"}`))
	rw1.Finish()

	mu.Lock()
	defer mu.Unlock()

	for _, msg := range loggedMessages {
		if strings.Contains(msg, "502") || strings.Contains(msg, "请求失败") || strings.Contains(msg, "已淘汰") {
			t.Fatalf("胜者已产生后，败方退出不应输出 502/失败报错日志，实际输出: %s", msg)
		}
	}

	// 确认包含温和的主动掐断提示
	var hasCancelMsg bool
	for _, msg := range loggedMessages {
		if strings.Contains(msg, "已随胜者出线主动掐断") {
			hasCancelMsg = true
			break
		}
	}
	if !hasCancelMsg {
		t.Fatalf("期望包含败方主动掐断提示，实际日志为: %v", loggedMessages)
	}
}


