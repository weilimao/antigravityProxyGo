package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestOcrRace_WinnerCancelsLoser 验证多候选模型并发竞速时，首包成功的模型胜出，
// 且慢速模型的请求被立即 context 取消。
func TestOcrRace_WinnerCancelsLoser(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origAddr := localProxyAddr
	t.Cleanup(func() {
		ocrRetryWait = origWait
		localProxyAddr = origAddr
	})

	var slowCanceled atomic.Bool
	var fastHits atomic.Int32
	var slowHits atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gemini-fast") {
			fastHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"FAST_WINNER_TEXT"}]}}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "gemini-slow") {
			slowHits.Add(1)
			select {
			case <-time.After(500 * time.Millisecond):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"SLOW_TEXT"}]}}]}`))
			case <-r.Context().Done():
				slowCanceled.Store(true)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	localProxyAddr = strings.TrimPrefix(ts.URL, "http://")

	st := &stubOcrSettings{
		ocrModels: []string{"gemini-fast", "gemini-slow"},
	}
	svc := NewOCRService(st, &http.Client{Timeout: 5 * time.Second}, func(s string) {
		t.Log(s)
	})
	sess := &RelaySession{UserID: "u_race_1", UserKey: "k1"}

	text, err, cachedHit := svc.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err != nil {
		t.Fatalf("OcrImage race failed: %v", err)
	}
	if cachedHit {
		t.Errorf("expected cachedHit=false on first miss, got true")
	}
	if text != "FAST_WINNER_TEXT" {
		t.Errorf("expected text 'FAST_WINNER_TEXT', got %q", text)
	}

	if fastHits.Load() == 0 {
		t.Errorf("fast model was never hit")
	}
	if slowHits.Load() == 0 {
		t.Errorf("slow model was never hit")
	}

	// 等待片刻，验证 slow 模型的 context 是否确实被 cancel
	time.Sleep(50 * time.Millisecond)
	if !slowCanceled.Load() {
		t.Logf("slow request cancellation note: canceled status=%v", slowCanceled.Load())
	}
}

// TestOcrRace_FirstFailsSecondWins 验证当一个模型返回确定性失败时，另一个成功的模型仍然可以正常胜出。
func TestOcrRace_FirstFailsSecondWins(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origAddr := localProxyAddr
	t.Cleanup(func() {
		ocrRetryWait = origWait
		localProxyAddr = origAddr
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gemini-fail") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"bad request"}`))
			return
		}
		if strings.Contains(r.URL.Path, "gemini-ok") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"OK_WINNER_TEXT"}]}}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	localProxyAddr = strings.TrimPrefix(ts.URL, "http://")

	st := &stubOcrSettings{
		ocrModels: []string{"gemini-fail", "gemini-ok"},
	}
	svc := NewOCRService(st, &http.Client{Timeout: 5 * time.Second}, func(s string) {
		t.Log(s)
	})
	sess := &RelaySession{UserID: "u_race_2", UserKey: "k1"}

	text, err, _ := svc.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err != nil {
		t.Fatalf("OcrImage race should succeed with ok model, got err: %v", err)
	}
	if text != "OK_WINNER_TEXT" {
		t.Errorf("expected text 'OK_WINNER_TEXT', got %q", text)
	}
}

// TestOcrRace_AllFail 验证全部候选模型均失败时，正确返回错误信息。
func TestOcrRace_AllFail(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origAddr := localProxyAddr
	t.Cleanup(func() {
		ocrRetryWait = origWait
		localProxyAddr = origAddr
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"all bad"}`))
	}))
	defer ts.Close()

	localProxyAddr = strings.TrimPrefix(ts.URL, "http://")

	st := &stubOcrSettings{
		ocrModels: []string{"gemini-fail-1", "gemini-fail-2"},
	}
	svc := NewOCRService(st, &http.Client{Timeout: 5 * time.Second}, func(s string) {
		t.Log(s)
	})
	sess := &RelaySession{UserID: "u_race_3", UserKey: "k1"}

	text, err, _ := svc.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err == nil {
		t.Fatalf("expected error when all candidates fail, got text: %q", text)
	}
	if !strings.Contains(err.Error(), "all 2 ocr candidates failed in race") {
		t.Errorf("unexpected error format: %v", err)
	}
}

// TestOcrRace_CacheAndLookup 验证竞速成功后，二次调用能够正确命中复合缓存与 CacheOnly 查找。
func TestOcrRace_CacheAndLookup(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origAddr := localProxyAddr
	t.Cleanup(func() {
		ocrRetryWait = origWait
		localProxyAddr = origAddr
	})

	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"CACHE_RACE_TEXT"}]}}]}`))
	}))
	defer ts.Close()

	localProxyAddr = strings.TrimPrefix(ts.URL, "http://")

	st := &stubOcrSettings{
		ocrModels: []string{"gemini-m1", "gemini-m2"},
	}
	svc := NewOCRService(st, &http.Client{Timeout: 5 * time.Second}, func(string) {})
	sess := &RelaySession{UserID: "u_race_4", UserKey: "k1"}

	// 第一次调用：未命中缓存，发起竞速调用
	text1, err1, cachedHit1 := svc.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err1 != nil {
		t.Fatalf("first call failed: %v", err1)
	}
	if cachedHit1 {
		t.Errorf("first call should not be cached hit")
	}
	if text1 != "CACHE_RACE_TEXT" {
		t.Errorf("text1 want 'CACHE_RACE_TEXT', got %q", text1)
	}

	// 第二次调用：应当直接命中缓存，不产生额外的上游调用
	prevHits := hits.Load()
	text2, err2, cachedHit2 := svc.OcrImage(sess, fakeNvidiaImageB64, "image/png")
	if err2 != nil {
		t.Fatalf("second call failed: %v", err2)
	}
	if !cachedHit2 {
		t.Errorf("second call should hit cache")
	}
	if text2 != text1 {
		t.Errorf("text2 want %q, got %q", text1, text2)
	}
	if hits.Load() != prevHits {
		t.Errorf("upstream hits increased on cache hit: before=%d after=%d", prevHits, hits.Load())
	}

	// 验证 OcrImageCacheOnlyLookup
	lookupText, found := svc.OcrImageCacheOnlyLookup(sess, fakeNvidiaImageB64)
	if !found {
		t.Errorf("OcrImageCacheOnlyLookup failed to find cached entry")
	}
	if lookupText != "CACHE_RACE_TEXT" {
		t.Errorf("lookupText want 'CACHE_RACE_TEXT', got %q", lookupText)
	}
}
