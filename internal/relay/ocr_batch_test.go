package relay

// ocr_batch_test.go —— 锁定 OCR 多图单请求批量上游(L1 批量原语)的契约。
//
// 覆盖 ocr_batch.go:
//   - splitBatchOcrText:按 [[图k]](1..n)拆 n 段,全到 ok=true / 缺任一 ok=false;
//   - buildBatchOcrPrompt / batchMarkerRule:文案含本批图片数、[[图k]] 输出约定;
//   - OcrImageBatch(L1 端到端,Google 族 Gemini 多 InlineData):N 张 miss 图合并 1 次上游
//     (hitUpstream==1,证明 N→1 生效),拆分后逐张回填各自 cache key(二次同批全命中、上游零触达);
//   - OcrImageBatch(L1 端到端,/route OpenAI Chat 多 image_url):一次请求含 N 个 image_url +
//     X-Antigravity-OCR-Self:1 自递归守卫头,响应按标记拆分;
//   - 拆分失败回退逐图:批量响应不含标记 → ok=false → 整批回退调原 OcrImage,上游 hit = 1(批量)+N(逐图),
//     各图文本正确、无占位回归(结果质量不劣于现状);
//   - 重试契约:批量 attempt 首调 503(可重试)、第 2 次 200(按标记回包)→ 成功;400 确定性不重试;
//   - 缓存契约:批量成功逐张写 success 长 TTL;二次全命中上游零触达;切 ocrModel 键变 → 重新批量;
//   - 混合命中/未命中:hit 项不进批量,仅 miss 项合并上游(batch 内去重同 b64);
//   - 边界:nil 服务 / nil 会话 / 空入参 → 空 result(不 panic)。
//
// 复用同包 fixtures:fakeNvidiaImageB64 / jsonString / newImageTestHandler / newCrossPoolOCRService
// / stubOcrSettings / localProxyAddr+localRelayAddr 覆盖 + t.Cleanup / ocrRetryWait 覆盖。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 1. 纯函数:splitBatchOcrText =====

func TestSplitBatchOcrText_AllFound(t *testing.T) {
	text := "[[图1]]第一张的分析\n代码块\n[[图2]]第二张"
	segs, ok := splitBatchOcrText(text, 2)
	if !ok {
		t.Fatalf("want ok=true for all markers present, got false")
	}
	if len(segs) != 2 {
		t.Fatalf("len segs want 2 got %d", len(segs))
	}
	if segs[0] != "第一张的分析\n代码块" {
		t.Errorf("seg1 wrong: %q", segs[0])
	}
	if segs[1] != "第二张" {
		t.Errorf("seg2 wrong: %q", segs[1])
	}
}

func TestSplitBatchOcrText_TrimWhitespace(t *testing.T) {
	// 段首尾空白 / 多换行应被 TrimSpace 清理。
	text := "[[图1]]\n\n  前后带空白  \n\n[[图2]]\n\t第二段\t\n"
	segs, ok := splitBatchOcrText(text, 2)
	if !ok {
		t.Fatalf("want ok=true")
	}
	if segs[0] != "前后带空白" {
		t.Errorf("seg1 trim wrong: %q", segs[0])
	}
	if segs[1] != "第二段" {
		t.Errorf("seg2 trim wrong: %q", segs[1])
	}
}

func TestSplitBatchOcrText_MissingMarker(t *testing.T) {
	// 缺 [[图2]] → 任一缺失即 ok=false,segs 为空(L2 对整批回退逐图)。
	text := "[[图1]]只有第一张"
	segs, ok := splitBatchOcrText(text, 2)
	if ok {
		t.Fatalf("want ok=false when [[图2]] missing, got true segs=%v", segs)
	}
	if segs != nil {
		t.Errorf("segs want nil on failure, got %v", segs)
	}
}

func TestSplitBatchOcrText_OutOfOrderFails(t *testing.T) {
	// 模型把图序号写乱([[图2]] 在 [[图1]] 之前)→ 顺序扫描找 [[图1]] 时其后无 [[图2]] → ok=false。
	text := "[[图2]]第二张[[图1]]第一张"
	_, ok := splitBatchOcrText(text, 2)
	if ok {
		t.Fatal("out-of-order markers should fail (顺序扫描锁定)")
	}
}

func TestSplitBatchOcrText_SingleAndZero(t *testing.T) {
	// n=1:仅 [[图1]] → ok=true,1 段到文末。
	segs, ok := splitBatchOcrText("[[图1]]唯一一张", 1)
	if !ok || len(segs) != 1 || segs[0] != "唯一一张" {
		t.Errorf("n=1 case wrong: segs=%v ok=%v", segs, ok)
	}
	// n=0:直接判失败(无意义批量)。
	if _, ok := splitBatchOcrText("anything", 0); ok {
		t.Error("n=0 should return ok=false")
	}
}

// ===== 2. 纯函数:buildBatchOcrPrompt / batchMarkerRule =====

func TestBuildBatchOcrPrompt_ContainsCountAndMarkerRule(t *testing.T) {
	p := buildBatchOcrPrompt("帮我看看这个报错", 3)
	// 必须含本批图片数(N)。
	if !strings.Contains(p, "本批共包含 3 张图片") {
		t.Errorf("prompt should mention batch count 3: %s", p)
	}
	// 必须含 [[图k]] 标记说明(格式约定)。
	if !strings.Contains(p, "[[图1]]") {
		t.Errorf("prompt should reference [[图1]] marker: %s", p)
	}
	if !strings.Contains(p, "[[图2]]") {
		t.Errorf("prompt should reference [[图2]] example marker: %s", p)
	}
	// 上限 N 应出现在"从 1 到 N"与"1,2,...,N"两处(确认 batchMarkerRule 按本批数生成)。
	if !strings.Contains(p, "从 1 到 3") || !strings.Contains(p, "1,2,...,3") {
		t.Errorf("prompt should reference upper count 3, got: %s", p)
	}
	// 必须含用户提问上下文(靶向分析方向)。
	if !strings.Contains(p, "帮我看看这个报错") {
		t.Errorf("prompt should embed promptCtx: %s", p)
	}
	// batchMarkerRule 应明确"按 1..N 顺序输出所有 N 段"。
	if !strings.Contains(p, "不得跳号") || !strings.Contains(p, "不得遗漏") {
		t.Errorf("prompt should enforce no-skip rule: %s", p)
	}
}

func TestBuildBatchOcrPrompt_NoPromptCtxUsesGenericTemplate(t *testing.T) {
	p := buildBatchOcrPrompt("", 2)
	// 无 promptCtx 时不拼"用户提问上下文"段,但仍有本批数 + 标记约定。
	if strings.Contains(p, "用户提问上下文") {
		t.Errorf("empty promptCtx should not embed user-question section: %s", p)
	}
	if !strings.Contains(p, "本批共包含 2 张图片") {
		t.Errorf("prompt should still mention count 2: %s", p)
	}
}

func TestBatchMarkerRule_FormatStable(t *testing.T) {
	r := batchMarkerRule(4)
	// 标记前缀、序号上限、严格顺序三个关键约定在文案里。
	for _, want := range []string{"[[图", "1", "4", "严格顺序"} {
		if !strings.Contains(r, want) {
			t.Errorf("batchMarkerRule(4) missing %q: %s", want, r)
		}
	}
}

// ===== 3. L1 端到端(Google 族 Gemini 多 InlineData)=====

// newBatchGeminiCountingMock 构造一个 mock 本地 Gemini generateContent 服务:
// 按 contents[0].Parts 里 InlineData 的数量回带 [[图k]] 标记的批量响应;hitUpstream 原子计数真实触达次数。
func newBatchGeminiCountingMock(t *testing.T, hitUpstream *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitUpstream.Add(1)
		body, _ := io.ReadAll(r.Body)
		var gemReq GeminiRequest
		_ = json.Unmarshal(body, &gemReq)
		nImg := 0
		if len(gemReq.Contents) > 0 {
			for _, p := range gemReq.Contents[0].Parts {
				if p.InlineData != nil && p.InlineData.Data != "" {
					nImg++
				}
			}
		}
		text := ""
		for k := 1; k <= nImg; k++ {
			text += fmt.Sprintf("[[图%d]]图%02d识别结果\n", k, k)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%s}]}}]}`, jsonString(text))
		w.Write([]byte(resp))
	}))
}

// TestOcrImageBatch_GoogleFamily_NTo1Upstream 锁定 Google 族批量 N→1:
// 2 张窗内 miss 图合并进 1 次上游调用(hitUpstream==1),拆分后逐张回填各自文本 + cache key;
// 二次同批两图全命中缓存,上游不再触达(hitUpstream 仍 1)。
func TestOcrImageBatch_GoogleFamily_NTo1Upstream(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := newBatchGeminiCountingMock(t, &hitUpstream)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	img1 := fakeNvidiaImageB64
	img2 := fakeNvidiaImageB64 + "AA" // 不同的 b64 → 不同 cache key,不触发批内去重

	// 第 1 轮:2 张全 miss → 合并 1 次上游(N→1)。
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: img1, Mime: "image/png"},
		{B64: img2, Mime: "image/jpeg"},
	})
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("batch upstream should be hit exactly 1 (N→1), got %d", got)
	}
	if len(results) != 2 {
		t.Fatalf("results len want 2 got %d", len(results))
	}
	for i, res := range results {
		if !res.Ok || res.CachedHit {
			t.Errorf("result %d should be Ok=true CachedHit=false, got %+v", i, res)
		}
		if res.Err != nil {
			t.Errorf("result %d unexpected err: %v", i, res.Err)
		}
	}
	if !strings.Contains(results[0].Text, "图01识别结果") {
		t.Errorf("img1 text should be [[图1]] segment, got %q", results[0].Text)
	}
	if !strings.Contains(results[1].Text, "图02识别结果") {
		t.Errorf("img2 text should be [[图2]] segment, got %q", results[1].Text)
	}
	// 计数契约:2 张 miss → counters.misses==2、hits==0。
	hits, misses := h.ocr.counters.snapshot()
	if hits != 0 || misses != 2 {
		t.Errorf("round1 counters want hits=0 misses=2, got hits=%d misses=%d", hits, misses)
	}

	// 第 2 轮:同批两图全命中缓存 → 上游不再触达(hitUpstream 仍 1),CachedHit=true。
	results2 := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: img1, Mime: "image/png"},
		{B64: img2, Mime: "image/jpeg"},
	})
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("round2 cache hit should NOT hit upstream, got %d", got)
	}
	for i, res := range results2 {
		if !res.CachedHit || !res.Ok {
			t.Errorf("round2 result %d should be CachedHit=true Ok=true, got %+v", i, res)
		}
	}
	// 计数契约:二次两图全命中 → counters.hits==2、misses 仍 2。
	hits, misses = h.ocr.counters.snapshot()
	if hits != 2 || misses != 2 {
		t.Errorf("round2 counters want hits=2 misses=2, got hits=%d misses=%d", hits, misses)
	}
}

// TestOcrImageBatch_GoogleFamily_MixedHitMiss 锁定混合命中/未命中:
// 一批里 img1 已缓存(不进批量)、img2 未命中(进批量)→ 上游只为 miss 项合并 1 次调用(此处 1 张),
// hit 项 CachedHit=true、miss 项 Ok=true,各自文本正确。
func TestOcrImageBatch_GoogleFamily_MixedHitMiss(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := newBatchGeminiCountingMock(t, &hitUpstream)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	img1 := fakeNvidiaImageB64
	img2 := "QUJDREVG" // 不同于 img1 的第二张图

	// 预热:先单独把 img1 OCR 一次,写入 success 长 TTL 缓存。
	if _, err, _ := h.ocr.OcrImage(sess, img1, "image/png"); err != nil {
		t.Fatalf("preheat img1 err: %v", err)
	}
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("preheat should hit upstream once, got %d", got)
	}

	// 批量:[img1(已缓存), img2(miss)] → 仅 img2 进批量,上游 +1(共 2)。
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: img1, Mime: "image/png"},
		{B64: img2, Mime: "image/png"},
	})
	if got := hitUpstream.Load(); got != 2 {
		t.Fatalf("mixed batch should add exactly 1 upstream (only miss item), got %d", got)
	}
	if !results[0].CachedHit || !results[0].Ok {
		t.Errorf("img1 should be CachedHit=true Ok=true, got %+v", results[0])
	}
	if results[1].CachedHit {
		t.Errorf("img2 should be miss (CachedHit=false), got %+v", results[1])
	}
	if !results[1].Ok || strings.TrimSpace(results[1].Text) == "" {
		t.Errorf("img2 should be Ok=true with text, got %+v", results[1])
	}
}

// TestOcrImageBatch_BatchDedupSameB64 锁定批内去重:一批里两张相同 b64 图,上游只发 1 次
// 该图(uniqN=1),拆分后把同一结果回填给两个位置,均 Ok。
func TestOcrImageBatch_BatchDedupSameB64(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := newBatchGeminiCountingMock(t, &hitUpstream)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	// 两张完全相同的 b64 → 批内去重 uniqN=1。
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: fakeNvidiaImageB64, Mime: "image/png"},
		{B64: fakeNvidiaImageB64, Mime: "image/png"},
	})
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("dedup batch should hit upstream exactly once (uniqN=1), got %d", got)
	}
	// 两个结果都应回填同一文本且 Ok。
	if !results[0].Ok || !results[1].Ok {
		t.Errorf("both dedup results should be Ok, got %+v", results)
	}
	if results[0].Text != results[1].Text {
		t.Errorf("dedup results should share identical text, got %q vs %q", results[0].Text, results[1].Text)
	}
}

// ===== 4. L1 端到端(/route OpenAI Chat 多 image_url)=====

// TestOcrImageBatch_RouteFamily_NImageURLsAndGuardHeader 锁定 /route 批量:
// 一次请求含 N 个 image_url + X-Antigravity-OCR-Self:1 自递归守卫头,响应按标记拆分回填。
func TestOcrImageBatch_RouteFamily_NImageURLsAndGuardHeader(t *testing.T) {
	var hitUpstream int32
	var capturedBody map[string]interface{}
	var capturedSelf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitUpstream, 1)
		capturedSelf = r.Header.Get("X-Antigravity-OCR-Self")
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedBody = body
		// 按入站 image_url 数量回带标记。
		nImg := 0
		if msgs, ok := body["messages"].([]interface{}); ok && len(msgs) > 0 {
			m := msgs[0].(map[string]interface{})
			if content, ok := m["content"].([]interface{}); ok {
				for _, c := range content {
					if mm, ok := c.(map[string]interface{}); ok && mm["type"] == "image_url" {
						nImg++
					}
				}
			}
		}
		text := ""
		for k := 1; k <= nImg; k++ {
			text += fmt.Sprintf("[[图%d]]route图%02d结果\n", k, k)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"choices":[{"message":{"content":%s}}]}`, jsonString(text))))
	}))
	defer srv.Close()

	orig := localRelayAddr
	localRelayAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localRelayAddr = orig })

	// routeResolver 把任意 model 解析为非 Google 族 → 走 /route 批量路径。
	h := newCrossPoolOCRService(t, func(model string) (string, string, string, bool) {
		return "nvidia", "", "gpt-4o", true
	})
	sess := &RelaySession{UserKey: "k1", UserID: "u1"}

	img1 := "QUJDREVG"
	img2 := "R0lGODlh"
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: img1, Mime: "image/png"},
		{B64: img2, Mime: "image/jpeg"},
	})
	if got := atomic.LoadInt32(&hitUpstream); got != 1 {
		t.Fatalf("route batch should hit upstream exactly once, got %d", got)
	}
	// 守卫头必须在(自递归守卫语义)。
	if capturedSelf != "1" {
		t.Errorf("X-Antigravity-OCR-Self want 1 got %q", capturedSelf)
	}
	// 一次请求含 2 个 image_url。
	msgs, _ := capturedBody["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	nImg := 0
	for _, c := range content {
		if mm, ok := c.(map[string]interface{}); ok && mm["type"] == "image_url" {
			nImg++
		}
	}
	if nImg != 2 {
		t.Errorf("route batch request should carry 2 image_url, got %d", nImg)
	}
	// 拆分后两图各自落文本([[图1]] / [[图2]])。
	if !results[0].Ok || !strings.Contains(results[0].Text, "route图01结果") {
		t.Errorf("img1 result wrong: %+v", results[0])
	}
	if !results[1].Ok || !strings.Contains(results[1].Text, "route图02结果") {
		t.Errorf("img2 result wrong: %+v", results[1])
	}
}

// ===== 5. 解析失败回退逐图 =====

// TestOcrImageBatch_ParseFailureFallsBackToPerImage 锁定:批量响应不含 [[图k]] 标记 →
// splitBatchOcrText 返回 ok=false → 整批回退调用原 OcrImage 逐图。上游总触达 = 1(失败批量) + N(逐图);
// 各图文本正确、无占位回归(结果质量不劣于现状)。
func TestOcrImageBatch_ParseFailureFallsBackToPerImage(t *testing.T) {
	var hitUpstream atomic.Int64
	// mock 永远回不带标记的纯文本(模拟模型不按标记输出)。
	// 注意:必须读完并 Close r.Body,避免 keep-alive 复用连接时未消费的请求体污染后续响应流,
	// 导致第二次(逐图回退)Do 在读响应体时撞到前一个请求残体 → "invalid character '}'" 解码失败。
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		hitUpstream.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// 固定纯文本,无 [[图k]] → 批量拆分失败 → 回退逐图。
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"逐图回退文本:报错 ERROR"}]}}]}`))
	}))
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	img1 := fakeNvidiaImageB64
	img2 := fakeNvidiaImageB64 + "BB" // 不同的 b64 → 逐图回退各打一次
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: img1, Mime: "image/png"},
		{B64: img2, Mime: "image/png"},
	})
	// 1 次失败批量 + 2 次逐图回退 = 3 次上游触达。
	if got := hitUpstream.Load(); got != 3 {
		t.Fatalf("fallback path should hit upstream 1(batch)+2(per-image)=3, got %d", got)
	}
	// 两图各自拿到回退后的文本(无占位回归)。
	for i, res := range results {
		if !res.Ok || res.Err != nil {
			t.Errorf("result %d should be Ok=true (fallback per-image succeeded), got %+v", i, res)
		}
		if !strings.Contains(res.Text, "逐图回退文本") {
			t.Errorf("result %d text should contain fallback OCR text, got %q", i, res.Text)
		}
	}
}

// ===== 6. 重试契约(批量 attempt)=====

// TestOcrImageBatch_RetryOn503ThenSuccess 批量首调 503(可重试)、第 2 次 200(按标记回包)→ 成功。
// 锁定批量 attempt 接入 ocrCallWithRetry 的瞬时失败重试语义。
func TestOcrImageBatch_RetryOn503ThenSuccess(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	origTimeout := ocrRetryTotalTimeout
	ocrRetryTotalTimeout = 5 * time.Second
	t.Cleanup(func() {
		ocrRetryWait = origWait
		ocrRetryTotalTimeout = origTimeout
	})

	var hitUpstream int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hitUpstream, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 触发重试
			w.Write([]byte(`{"error":"down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[[图1]]重试成功\n[[图2]]第二张"}]}}]}`))
	}))
	defer srv.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: "IMG-A", Mime: "image/png"},
		{B64: "IMG-B", Mime: "image/png"},
	})
	if got := atomic.LoadInt32(&hitUpstream); got != 2 {
		t.Fatalf("503-then-200 should hit upstream exactly 2 times (1 retry), got %d", got)
	}
	if !results[0].Ok || !strings.Contains(results[0].Text, "重试成功") {
		t.Errorf("img1 should succeed after retry: %+v", results[0])
	}
	if !results[1].Ok || !strings.Contains(results[1].Text, "第二张") {
		t.Errorf("img2 should succeed after retry: %+v", results[1])
	}
}

// TestOcrImageBatch_Deterministic400NoRetry 批量上游返回 400(确定性)→ 不重试,只触达 1 次,失败。
func TestOcrImageBatch_Deterministic400NoRetry(t *testing.T) {
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	var hitUpstream int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitUpstream, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{
		{B64: "IMG-A", Mime: "image/png"},
		{B64: "IMG-B", Mime: "image/png"},
	})
	// 400 确定性 → 批量只触达 1 次(不重试);批量失败后回退逐图 OcrImage 每张 400 也不重试 → 共 1+2=3。
	if got := atomic.LoadInt32(&hitUpstream); got != 3 {
		t.Fatalf("400 deterministic: batch 1 + per-image 2 = 3 upstream hits, got %d", got)
	}
	// 全部失败(回退逐图也是 400)。
	for i, res := range results {
		if res.Ok {
			t.Errorf("result %d should fail (400), got Ok=true %+v", i, res)
		}
		if res.Err == nil {
			t.Errorf("result %d should carry err, got nil", i)
		}
	}
}

// ===== 7. 缓存契约:切 ocrModel 键变 → 重新批量 =====

// TestOcrImageBatch_SwitchOcrModelReBatch 锁定:批量成功逐张写 success 长 TTL 后,
// 切换 ocrModel → cache key 含 ocrModel 维 → 键变 → 全 miss → 重新批量上游。
func TestOcrImageBatch_SwitchOcrModelReBatch(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := newBatchGeminiCountingMock(t, &hitUpstream)
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	// 用可配 ocrModel 的 settings 构造 handler,首模型 gemini-2.5-flash。
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, &stubOcrSettings{ocrModel: "gemini-2.5-flash"}, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}

	items := []OcrBatchItem{
		{B64: fakeNvidiaImageB64, Mime: "image/png"},
		{B64: fakeNvidiaImageB64 + "CC", Mime: "image/jpeg"},
	}
	// 第 1 轮:flash 模型,2 张 miss → 上游 1 次。
	r1 := h.ocr.OcrImageBatch(sess, items)
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("round1 should hit 1, got %d", got)
	}
	for i, res := range r1 {
		if !res.Ok {
			t.Errorf("round1 result %d should Ok, got %+v", i, res)
		}
	}

	// 第 2 轮:同 flash 模型 → 全命中缓存,上游仍 1。
	h.ocr.OcrImageBatch(sess, items)
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("round2 cache hit should not hit upstream, got %d", got)
	}

	// 切 ocrModel 为 gemini-2.5-pro → 键变 → 全 miss → 重新批量上游 +1(共 2)。
	h.ocr.settingsMgr = &stubOcrSettings{ocrModel: "gemini-2.5-pro"}
	r3 := h.ocr.OcrImageBatch(sess, items)
	if got := hitUpstream.Load(); got != 2 {
		t.Fatalf("switching ocrModel should re-batch (hit 2), got %d", got)
	}
	for i, res := range r3 {
		if !res.Ok {
			t.Errorf("round3 result %d should Ok after re-batch, got %+v", i, res)
		}
		if res.CachedHit {
			t.Errorf("round3 result %d should be miss (new model key), got CachedHit=true", i)
		}
	}
}

// ===== 8. 边界:nil 服务 / nil 会话 / 空入参 =====

func TestOcrImageBatch_NilSessionReturnsEmpty(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	results := h.ocr.OcrImageBatch(nil, []OcrBatchItem{{B64: "x", Mime: "image/png"}})
	if len(results) != 1 {
		t.Fatalf("nil session should return len(items) results, got %d", len(results))
	}
	if results[0].Ok || results[0].Err != nil {
		t.Errorf("nil session result should be zero (Ok=false Err=nil), got %+v", results[0])
	}
}

func TestOcrImageBatch_EmptyItemsReturnsEmpty(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	results := h.ocr.OcrImageBatch(&RelaySession{UserID: "u1"}, nil)
	if len(results) != 0 {
		t.Fatalf("empty items should return empty slice, got %d", len(results))
	}
}

func TestOcrImageBatch_EmptyB64IsError(t *testing.T) {
	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	results := h.ocr.OcrImageBatch(&RelaySession{UserID: "u1", UserKey: "k1"}, []OcrBatchItem{
		{B64: "   ", Mime: "image/png"},
	})
	if len(results) != 1 {
		t.Fatalf("len want 1 got %d", len(results))
	}
	if results[0].Ok || results[0].Err == nil {
		t.Errorf("empty b64 should yield Ok=false with err, got %+v", results[0])
	}
}

// ===== 9. batchUpstream 直接契约(仍走真实 OcrImageBatch,聚焦上游错误文本分类)=====

// TestBatchUpstream_ErrorTextContainsStatusKeyword 锁定批量上游错误文本含 "status " 关键词,
// 保证 retryableStatusFromErr / isOcrRetryableErr 能分类重试语义(与单图 ocrImageUncached 同口径)。
func TestBatchUpstream_ErrorTextContainsStatusKeyword(t *testing.T) {
	// 用一个直接返回 502 的 mock,经 OcrImageBatch 触发 batchUpstream。
	// 502 可重试 → 跑满 ocrMaxAttempts 后回退逐图;最终 errors 文本含 "status"。
	origWait := ocrRetryWait
	ocrRetryWait = time.Millisecond
	t.Cleanup(func() { ocrRetryWait = origWait })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"bad gateway"}`))
	}))
	defer srv.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	h := NewAPICompatHandler(nil, nil, nil, nil, nil, nil, nil)
	sess := &RelaySession{UserID: "u1", UserKey: "k1"}
	results := h.ocr.OcrImageBatch(sess, []OcrBatchItem{{B64: "IMG", Mime: "image/png"}})
	if len(results) != 1 {
		t.Fatalf("len want 1 got %d", len(results))
	}
	// 502 重试全败 + 回退逐图 OcrImage 也 502 重试全败 → 最终 err 含 "status 502"。
	if results[0].Err == nil {
		t.Fatal("want err for 502 exhausted, got nil")
	}
	if !strings.Contains(results[0].Err.Error(), "status 502") {
		t.Errorf("err should contain 'status 502' keyword for retry classification, got %v", results[0].Err)
	}
}

// ===== 10. buildBatchGeminiRequest / buildBatchRouteRequest 请求体形态契约 =====

func TestBuildBatchGeminiRequest_Shape(t *testing.T) {
	req := buildBatchGeminiRequest("prompt", []string{"b64a", "b64b"}, []string{"image/png", "image/jpeg"})
	if len(req.Contents) != 1 {
		t.Fatalf("contents want 1 got %d", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Errorf("role want user got %s", req.Contents[0].Role)
	}
	parts := req.Contents[0].Parts
	// 第一个 Part 是 prompt 文本,其后 N 个是 InlineData(按顺序)。
	if len(parts) != 3 {
		t.Fatalf("parts want 1+2=3 got %d", len(parts))
	}
	if parts[0].Text != "prompt" || parts[0].InlineData != nil {
		t.Errorf("part0 should be text prompt, got %+v", parts[0])
	}
	if parts[1].InlineData == nil || parts[1].InlineData.Data != "b64a" || parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("part1 inline wrong: %+v", parts[1])
	}
	if parts[2].InlineData == nil || parts[2].InlineData.Data != "b64b" || parts[2].InlineData.MimeType != "image/jpeg" {
		t.Errorf("part2 inline wrong: %+v", parts[2])
	}
}

func TestBuildBatchGeminiRequest_EmptyMimeFallsBack(t *testing.T) {
	req := buildBatchGeminiRequest("p", []string{"b64"}, []string{""})
	if req.Contents[0].Parts[1].InlineData.MimeType != "image/jpeg" {
		t.Errorf("empty mime should fall back to image/jpeg, got %s", req.Contents[0].Parts[1].InlineData.MimeType)
	}
}

func TestBuildBatchRouteRequest_Shape(t *testing.T) {
	bytes, err := buildBatchRouteRequest("google/gemini-2.5-flash", "prompt", []string{"b64a", "b64b"}, []string{"image/png", "image/jpeg"})
	if err != nil {
		t.Fatalf("marshal err: %v", err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(bytes, &obj); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if obj["model"] != "google/gemini-2.5-flash" {
		t.Errorf("model want preserved with prefix, got %v", obj["model"])
	}
	msgs := obj["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"].([]interface{})
	if len(content) != 3 {
		t.Fatalf("content want 1 text + 2 image_url = 3, got %d", len(content))
	}
	if content[0].(map[string]interface{})["type"] != "text" {
		t.Error("part0 should be text")
	}
	for i := 1; i <= 2; i++ {
		part := content[i].(map[string]interface{})
		if part["type"] != "image_url" {
			t.Errorf("part %d should be image_url", i)
		}
		url := part["image_url"].(map[string]interface{})["url"].(string)
		wantMime := "image/png"
		if i == 2 {
			wantMime = "image/jpeg"
		}
		want := "data:" + wantMime + ";base64,"
		if !strings.HasPrefix(url, want) {
			t.Errorf("part %d image_url want prefix %s got %s", i, want, url)
		}
	}
}

func TestBuildBatchRouteRequest_EmptyModelFallsBack(t *testing.T) {
	bytes, _ := buildBatchRouteRequest("", "p", []string{"b64"}, []string{"image/png"})
	var obj map[string]interface{}
	_ = json.Unmarshal(bytes, &obj)
	if obj["model"] != defaultOcrModel {
		t.Errorf("empty model should fall back to defaultOcrModel, got %v", obj["model"])
	}
}

// ===== 11. L2 端到端:Gemini DowngradeGeminiImagesToText 多图 N→1 =====

// TestDowngradeGeminiImagesToText_MultipleImages_NTo1 锁定 Gemini L2 多图批量:
// 同一条 message 内 ≥2 张 InlineData miss 图合并进一次上游 OcrImageBatch(OcrImageBatch 内 Google 族
// 多 InlineData 路径),而非逐图各打一次。mock 按入站 InlineData 数量回带 [[图k]] 标记;hitUpstream==1
// 证明 N→1 生效。降级后各 Parts[j] 的 InlineData 被清空、Text 拼接 OCR 描述,且文本落对各自图序。
func TestDowngradeGeminiImagesToText_MultipleImages_NTo1(t *testing.T) {
	var hitUpstream atomic.Int64
	ocr := newBatchGeminiCountingMock(t, &hitUpstream) // 按入站 InlineData 数回带 [[图k]] 标记
	defer ocr.Close()
	origAddr := localProxyAddr
	localProxyAddr = strings.TrimPrefix(ocr.URL, "http://")
	t.Cleanup(func() { localProxyAddr = origAddr })

	// 构造 OCRService:目标模型显式声明 Multimodal=false(经 mappingResolver 否决)→ 进入降级分支。
	// 用 mappingResolver(而非纯启发式)确保降级闸判定确定性强、不受启发式白名单维护影响。
	svc := NewOCRService(nil, &http.Client{Timeout: 5 * time.Second}, func(string) {})
	svc.cache = newOcrLRUCache(0, 0, 0)
	notMultimodal := false
	svc.SetMappingResolver(func(model string) (*bool, bool) {
		if model == "plain-text-model" {
			return &notMultimodal, true
		}
		return nil, false
	})

	img1 := fakeNvidiaImageB64
	img2 := fakeNvidiaImageB64 + "DD" // 不同的 b64 → 不同 cache key,不触发批内去重
	gemReq := &GeminiRequest{
		Contents: []GeminiContent{{
			Role: "user",
			Parts: []GeminiPart{
				{Text: "看这两张报错图"},
				{InlineData: &GeminiBlob{MimeType: "image/png", Data: img1}},
				{InlineData: &GeminiBlob{MimeType: "image/jpeg", Data: img2}},
			},
		}},
	}
	downgraded, ocrHits, ocrMisses, err := svc.DowngradeGeminiImagesToText(gemReq, &RelaySession{UserID: "u1", UserKey: "k1"}, "plain-text-model")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 批量契约:2 张 miss 图 → 上游只触达 1 次(N→1)。
	if got := hitUpstream.Load(); got != 1 {
		t.Fatalf("batch upstream should be hit exactly 1 (N→1), got %d", got)
	}
	if downgraded != 2 {
		t.Fatalf("downgraded want 2 (both images), got %d", downgraded)
	}
	if ocrHits != 0 || ocrMisses != 2 {
		t.Errorf("ocrHits/ocrMisses want 0/2, got %d/%d", ocrHits, ocrMisses)
	}
	parts := gemReq.Contents[0].Parts
	// Part0 仍是原文 text(不被覆盖)。
	if parts[0].Text != "看这两张报错图" {
		t.Errorf("part0 text corrupted: %q", parts[0].Text)
	}
	// Part1 / Part2 的 InlineData 应被清空、Text 含各自 OCR 描述(图1 / 图2 标记对应)。
	for i := 1; i <= 2; i++ {
		if parts[i].InlineData != nil {
			t.Errorf("part %d InlineData should be cleared", i)
		}
		if parts[i].Text == "" {
			t.Errorf("part %d Text should be filled with OCR desc", i)
		}
	}
	want1 := "图01识别结果"
	if !strings.Contains(parts[1].Text, want1) {
		t.Errorf("part1 (img1) should contain %q, got %q", want1, parts[1].Text)
	}
	want2 := "图02识别结果"
	if !strings.Contains(parts[2].Text, want2) {
		t.Errorf("part2 (img2) should contain %q, got %q", want2, parts[2].Text)
	}
}

// 占位:确保 context 包在合成 attempt 闭包被需要时编译(目前 batch 重试经真实 httptest,
// 但保留 import 以便后续扩展合成 RT 测试)。
var _ = context.Background
