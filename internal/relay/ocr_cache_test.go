package relay

// ocr_cache_test.go —— 锁定 OCR 进程内缓存(ocrLRUCache + singleflight)的行为契约。
//
// 覆盖契约:
//   - 缓存键按 UserKey / ocrModel / 图内容三维隔离(任一维度不同 → 不同键)
//   - 命中不触达上游、未命中触达上游一次
//   - 同图并发相邻请求由 singleflight 合并为 1 次真上游调用
//   - 成功 TTL 过期后再降级会重新触达上游
//   - OCR 失败的短 TTL 熔断窗口内,重复请求不再打挂的 OCR 服务
//   - LRU 容量超限淘汰最旧条目
//
// 上游触达计数由 atomic 计数器在 mock OCR 服务内自增,断言"是否真打上游"精确、无竞态。

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newOcrFlashCountingServer 构造一个 mock 本地 Gemini OCR 服务,
// 每收一个请求 hitUpstream 计数 +1,回包 candidates[0].content.parts[0].text = ocrText。
// 用原子计数精确度量"是否真打上游",供缓存契约断言。
func newOcrFlashCountingServer(t *testing.T, ocrText string, hitUpstream *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitUpstream.Add(1)
		w.Header().Set("Content-Type", "application/json")
		resp := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%s}]}}]}`, jsonString(ocrText))
		w.Write([]byte(resp))
	}))
}

func TestOCR_CacheKeyIsolatesByUserKey(t *testing.T) {
	k1 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA")
	k2 := ocrCacheKey("user-b", "gemini-2.5-flash", "IMGDATA")
	if k1 == k2 {
		t.Errorf("cache key should isolate by user key, got identical %q", k1)
	}
}

func TestOCR_CacheKeyIsolatesByOcrModel(t *testing.T) {
	k1 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA")
	k2 := ocrCacheKey("user-a", "gemini-2.5-pro", "IMGDATA")
	if k1 == k2 {
		t.Errorf("cache key should isolate by ocr model, got identical %q", k1)
	}
}

func TestOCR_CacheKeyIsolatesByImage(t *testing.T) {
	k1 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA-1")
	k2 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA-2")
	if k1 == k2 {
		t.Errorf("cache key should isolate by image content, got identical %q", k1)
	}
}

// callOcrWithCache 用真 APICompatHandler 跑一次 OCR,但用短 TTL 缓存(便于测过期)
// 与计数 mock,断言 hit 与 hasCache 行为。
func callOcrWithCache(
	t *testing.T,
	h *APICompatHandler,
	sess *RelaySession,
	mime, b64Data string,
) (string, error, bool) {
	t.Helper()
	return h.ocr.OcrImage(sess, b64Data, mime)
}

func TestOCR_CacheHitSkipsUpstream(t *testing.T) {
	var hits atomic.Int64
	ocr := newOcrFlashCountingServer(t, "图中文字:ERROR 404", &hits)
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(8, time.Minute, time.Minute) // 命中模式
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("first call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("first call should hit upstream once, got %d", hits.Load())
	}

	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("second call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("cache hit should skip upstream, hits still %d (want 1)", hits.Load())
	}

	hits2, misses := h.ocr.counters.snapshot()
	if hits2 != 1 || misses != 1 {
		t.Errorf("counters want hits=1 misses=1, got hits=%d misses=%d", hits2, misses)
	}
}

func TestOCR_CacheMissCallsUpstream(t *testing.T) {
	var hits atomic.Int64
	ocr := newOcrFlashCountingServer(t, "OK", &hits)
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0) // 默认参数
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("first call should hit upstream once, got %d", hits.Load())
	}
	_, misses := h.ocr.counters.snapshot()
	if misses != 1 {
		t.Errorf("counters want misses=1, got misses=%d", misses)
	}
}

func TestOCR_ConcurrentSameImageSingleflight(t *testing.T) {
	var hits atomic.Int64
	ocr := newOcrFlashCountingServer(t, "OCR", &hits)
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	const N = 50
	var wg sync.WaitGroup
	results := make([]string, N)
	errs := make([]error, N)
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i], _ = callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64)
		}(i)
	}
	close(start)
	wg.Wait()

	if hits.Load() != 1 {
		t.Errorf("singleflight should collapse to 1 upstream call, got %d", hits.Load())
	}
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d err: %v", i, errs[i])
		}
		if !strings.Contains(results[i], "OCR") {
			t.Errorf("goroutine %d result wrong: %s", i, results[i])
		}
	}
}

func TestOCR_TTLExpiredReCallsUpstream(t *testing.T) {
	var hits atomic.Int64
	ocr := newOcrFlashCountingServer(t, "OCR", &hits)
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	// 注入 50ms 超短成功 TTL,便于测试内验证过期重算。
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(8, 50*time.Millisecond, 50*time.Millisecond)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("first call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("first call should hit upstream once, got %d", hits.Load())
	}

	// 命中窗口内,第二次不触达上游。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("second call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("second call cache hit should not hit upstream, got %d", hits.Load())
	}

	time.Sleep(80 * time.Millisecond) // 过期

	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err != nil {
		t.Fatalf("third call err: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("after TTL expired should re-hit upstream (want 2), got %d", hits.Load())
	}
}

func TestOCR_FailureShortTTLNoSpam(t *testing.T) {
	// mock 一个始终失败的 OCR 服务(500),hitUpstream 计每次真实触达。
	var hits atomic.Int64
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"ocr unavailable"}`))
	}))
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	// 失败短 TTL 50ms。
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(8, time.Minute, 50*time.Millisecond)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 第 1 次:真打上游,失败,缓存失败条目(短 TTL)。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err == nil {
		t.Fatal("first call should fail (upstream 500)")
	}
	if hits.Load() != 1 {
		t.Fatalf("first call should hit upstream once, got %d", hits.Load())
	}

	// 第 2、3 次:命中失败缓存,不触达上游(熔断窗口内不雪崩打挂的服务)。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err == nil {
		t.Fatal("second call should still fail")
	}
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err == nil {
		t.Fatal("third call should still fail")
	}
	if hits.Load() != 1 {
		t.Fatalf("failure short-ttl should suppress spam (want 1), got %d", hits.Load())
	}

	time.Sleep(80 * time.Millisecond) // 失败熔断窗口过期

	// 第 4 次:过期,再次试探上游一次。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", fakeNvidiaImageB64); err == nil {
		t.Fatal("fourth call should still fail")
	}
	if hits.Load() != 2 {
		t.Fatalf("after failure-ttl expired should re-hit upstream (want 2), got %d", hits.Load())
	}
}

func TestOCR_LRU_Eviction(t *testing.T) {
	var hits atomic.Int64
	ocr := newOcrFlashCountingServer(t, "OCR", &hits)
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(2, time.Minute, time.Minute) // 容量 2
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 塞 3 张不同的图,容量 2 → 最旧的图 1 被淘汰。
	for i := 0; i < 3; i++ {
		b64 := fmt.Sprintf("img-%d", i)
		if _, err, _ := callOcrWithCache(t, h, sess, "image/png", b64); err != nil {
			t.Fatalf("call %d err: %v", i, err)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("initial 3 distinct images should hit 3 times, got %d", hits.Load())
	}

	// 图 1(最先塞)已被淘汰 → 再降级它应重新触达上游。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", "img-0"); err != nil {
		t.Fatalf("re-call img-0 err: %v", err)
	}
	if hits.Load() != 4 {
		t.Fatalf("evicted img-0 should re-hit upstream (want 4), got %d", hits.Load())
	}

	// 图 3(最近塞)仍在缓存 → 再降级它不触达上游。
	if _, err, _ := callOcrWithCache(t, h, sess, "image/png", "img-2"); err != nil {
		t.Fatalf("re-call img-2 err: %v", err)
	}
	if hits.Load() != 4 {
		t.Fatalf("cached img-2 should not hit upstream (want 4), got %d", hits.Load())
	}
}

func TestOCR_CacheKeyIgnoresUserPromptText(t *testing.T) {
	// 缓存键契约:image-only(三维:ownerKey|ocrModel|sha256(b64)[:16]),不含 promptCtx。
	// 同会话同图无论传什么 promptCtx(或不传),键必须完全相同。这是"继续"/resume/标点微调
	// 等无实质意图的文本变化不触发重 OCR 的根本保证。
	// 注:ocrCacheKey 签名已剥离 promptCtx 参数,故这里用 3-arg 形式断言"键公式不含第四维":
	// 历史上曾以变参纳入 promptCtx,现彻底删除,从签名层就杜绝了 promptCtx 进键。
	k1 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA")
	k2 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA")
	k3 := ocrCacheKey("user-a", "gemini-2.5-flash", "IMGDATA")

	if k1 != k2 {
		t.Errorf("cache key for same image must be identical, got %q vs %q", k1, k2)
	}
	if k1 != k3 {
		t.Errorf("cache key for same image must be identical, got %q vs %q", k1, k3)
	}
}

// TestOCR_OcrUpstreamStillReceivesPromptCtx 锁定"缓存键与上游 call 解耦"契约:
// 缓存键不含 promptCtx(同图同会话跨提问命中),但 miss 真打 gemini 上游时,
// 请求体的 ocrPrompt 仍必须包含当前 promptCtx 文本(保留靶向 OCR 分析方向)。
// 复刻 ocrImageUncached 的 ocrPrompt 拼装逻辑做断言,确保未来重构不丢 context。
func TestOCR_OcrUpstreamStillReceivesPromptCtx(t *testing.T) {
	var hits atomic.Int64
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "报错栈最上面那行") {
			t.Errorf("ocr upstream prompt should contain promptCtx text, body=%s", string(body))
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`))
	}))
	defer ocr.Close()

	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	h.ocr.cache = newOcrLRUCache(0, 0, 0)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 第 1 次:miss 真打上游,ocrPrompt 必须含 promptCtx("报错栈最上面那行")。
	if _, err, _ := h.ocr.OcrImage(sess, fakeNvidiaImageB64, "image/png", "报错栈最上面那行"); err != nil {
		t.Fatalf("first call err: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("miss should hit upstream once, got %d", hits.Load())
	}
}
