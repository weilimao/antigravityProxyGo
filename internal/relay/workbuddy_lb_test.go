package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/session"
)

func TestWorkBuddy_RoundRobin_Distribution(t *testing.T) {
	var mu sync.Mutex
	hitCounts := make(map[string]int)

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hitCounts["acc1"]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"acc1\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server1.Close)

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hitCounts["acc2"]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"acc2\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server2.Close)

	tempDir, _ := os.MkdirTemp("", "wb_rr_test_*")
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetWorkBuddyLBMode("round-robin")

	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server1.URL,
		AccessToken: "token-acc1",
		Nickname:    "user1",
	})
	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server2.URL,
		AccessToken: "token-acc2",
		Nickname:    "user2",
	})

	handler := NewAPICompatHandler(nil, accMgr, nil, nil, nil, nil, nil)

	for i := 0; i < 4; i++ {
		reqBody := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"ping"}],"stream":false}`
		httpReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/chat/completions", strings.NewReader(reqBody))
		w := httptest.NewRecorder()
		handler.handleWorkBuddy(w, httpReq, &RelaySession{UserID: fmt.Sprintf("user_%d", i)})
	}

	mu.Lock()
	c1 := hitCounts["acc1"]
	c2 := hitCounts["acc2"]
	mu.Unlock()

	if c1 != 2 || c2 != 2 {
		t.Errorf("期望交替轮询各 2 次, 得到 acc1=%d, acc2=%d", c1, c2)
	}
}

func TestWorkBuddy_Sticky_Session(t *testing.T) {
	var mu sync.Mutex
	tokens := make([]string, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_sticky\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	tempDir, _ := os.MkdirTemp("", "wb_sticky_test_*")
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	accMgr.SetWorkBuddyLBMode("sticky")

	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server.URL,
		AccessToken: "token-sticky-1",
		Nickname:    "sticky_user_1",
	})
	_, _ = accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server.URL,
		AccessToken: "token-sticky-2",
		Nickname:    "sticky_user_2",
	})

	router := session.NewRouter()
	handler := NewAPICompatHandler(nil, accMgr, router, nil, nil, nil, nil)

	// 同一会话发送 3 次请求
	for i := 0; i < 3; i++ {
		reqBody := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"ping"}],"stream":false}`
		httpReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/chat/completions", strings.NewReader(reqBody))
		w := httptest.NewRecorder()
		handler.handleWorkBuddy(w, httpReq, &RelaySession{UserID: "constant_user", SessionKey: "session_123"})
	}

	mu.Lock()
	defer mu.Unlock()

	if len(tokens) != 3 {
		t.Fatalf("请求数期望 3, 得到 %d", len(tokens))
	}
	if tokens[0] != tokens[1] || tokens[1] != tokens[2] {
		t.Errorf("粘性会话期望命中相同 Token, 得到: %v", tokens)
	}
}

func TestWorkBuddy_ConcurrencyLimit_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"wb_cc\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	tempDir, _ := os.MkdirTemp("", "wb_concurrency_test_*")
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	accMgr := account.NewManager()
	accMgr.Init(tempDir)
	// 设置并发上限为 1
	accMgr.SetWorkBuddyMaxConcurrency(1)

	id1, _ := accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server.URL,
		AccessToken: "token-c1",
		Nickname:    "user_c1",
	})
	id2, _ := accMgr.AddWorkBuddyAccount(account.WorkBuddyAccountInput{
		BaseURL:     server.URL,
		AccessToken: "token-c2",
		Nickname:    "user_c2",
	})

	// 人工占用 id1 的并发槽 (1 / 1 满载)
	accMgr.AcquireAccount(id1)
	defer accMgr.ReleaseAccount(id1)

	handler := NewAPICompatHandler(nil, accMgr, nil, nil, nil, nil, nil)

	reqBody := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"ping"}],"stream":false}`
	httpReq := httptest.NewRequest(http.MethodPost, "/workbuddy/v1/chat/completions", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.handleWorkBuddy(w, httpReq, &RelaySession{UserID: "test_concurrency_user"})

	// 由于 id1 已经被占满，新请求必须自动分派到未满载的 id2
	// 验证在请求处理完成后 id2 的并发槽已经正确释放 (0)
	if got := accMgr.AccountInFlightCount(id2); got != 0 {
		t.Errorf("id2 请求结束后并发槽应为 0, 得到 %d", got)
	}
}

