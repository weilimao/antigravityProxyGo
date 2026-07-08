package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/stats"
)

func TestProxyHandler_Timeout(t *testing.T) {
	// 1. 启动一个支持 TLS 且会延迟返回的测试服务器
	delaySrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意阻塞 3 秒，模拟慢响应
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success_response"))
	}))
	defer delaySrv.Close()

	srvURL, err := url.Parse(delaySrv.URL)
	if err != nil {
		t.Fatalf("Failed to parse server URL: %v", err)
	}

	// 2. 初始化 ProxyHandler 依赖桩
	accMgr := account.NewManager()
	sessionRouter := session.NewRouter()
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	// 3. 构造请求超时被配置为 1 秒的 ProxyHandler
	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyHandler Log] %s", s) }, // logFn
		nil,               // quotaFetch
		nil,               // tokenRefresh
		func(s1, s2 string) {}, // setCapturedProject
		func(s string) string { return "" }, // getStoredProject
		func() int { return 0 },             // getMaxRetries
		func() int { return 1 },             // getMaxRetryDelay
		func() int64 { return 1024 * 1024 }, // 1MB
		func() int { return 1 },             // getRequestTimeout (设为 1 秒)
		nil, // relayStatsCallback
		nil, // relayQuotaCheck
	)

	// 4. 关键：获取并配置测试服务器的 client
	srvClient := delaySrv.Client()
	srvClient.Timeout = 0
	
	// 通过自定义 DialContext 将所有拨号请求重定向到测试服务器的实际端口
	transport := srvClient.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, srvURL.Host)
	}
	// 忽略证书域名校验，使得连到 generativelanguage 也能通过自签名证书校验
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	
	handler.client = srvClient

	// 5. 构造普通的健康检查 API 请求，避免触发推理请求的账号池检测
	req := httptest.NewRequest(http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/health", nil)
	req.Host = "generativelanguage.googleapis.com"

	// 使用 httptest 录制响应
	rec := httptest.NewRecorder()

	startTime := time.Now()
	// 执行 ProxyHandler
	handler.ServeHTTP(rec, req)
	duration := time.Since(startTime)

	// 6. 断言验证：
	// 因为配置的超时是 1 秒，而 Mock 服务端延迟 3 秒，因此它应该在 1.0 ~ 1.5 秒内即刻返回
	// 如果低于 800ms 说明没有成功建立连接；如果大于 2000ms 说明超时未生效。
	if duration < 800*time.Millisecond {
		t.Errorf("Request failed too early (likely connection issue), duration: %v", duration)
	}
	if duration >= 2000*time.Millisecond {
		t.Errorf("Request was not timed out early, duration: %v, expected < 2s", duration)
	}

	// 此时因为超时熔断，Context 取消，返回的状态码应为 502 Bad Gateway
	if rec.Code != http.StatusBadGateway {
		t.Errorf("Expected HTTP status 502 (Bad Gateway) on timeout, got: %d", rec.Code)
	}

	t.Logf("Result HTTP Code: %d, Response Body: %s", rec.Code, rec.Body.String())
	t.Logf("Test passed. Early timeout triggered successfully. Request took %v", duration)
}

func TestProxyHandler_StreamingCancellation(t *testing.T) {
	// 1. 启动一个支持 Stream 流式输出的 Mock 测试服务器
	streamSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// 循环输出数据块，间隔 50ms
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				// 正常监听到客户端断开
				return
			default:
				_, _ = w.Write([]byte(fmt.Sprintf("data: chunk %d\n\n", i)))
				flusher.Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}
	}))
	defer streamSrv.Close()

	srvURL, err := url.Parse(streamSrv.URL)
	if err != nil {
		t.Fatalf("Failed to parse server URL: %v", err)
	}

	// 2. 初始化 ProxyHandler 依赖桩
	accMgr := account.NewManager()
	sessionRouter := session.NewRouter()
	pricingMgr := pricing.NewManager()
	statsTracker := stats.NewTracker(pricingMgr)
	usageTracker := stats.NewUsageTracker(pricingMgr)
	errLogger := stats.NewRetryErrorLogger()
	packetCap := stats.NewPacketCapturer(nil, nil, func() bool { return false })

	handler := NewProxyHandler(
		accMgr,
		sessionRouter,
		statsTracker,
		usageTracker,
		errLogger,
		packetCap,
		func(s string) { t.Logf("[ProxyHandler Log] %s", s) }, // logFn
		nil,               // quotaFetch
		nil,               // tokenRefresh
		func(s1, s2 string) {}, // setCapturedProject
		func(s string) string { return "" }, // getStoredProject
		func() int { return 0 },             // getMaxRetries
		func() int { return 1 },             // getMaxRetryDelay
		func() int64 { return 1024 * 1024 }, // 1MB
		func() int { return 30 },            // getRequestTimeout
		nil, // relayStatsCallback
		nil, // relayQuotaCheck
	)

	// 3. 配置测试服务器的 client 到 handler.client
	srvClient := streamSrv.Client()
	srvClient.Timeout = 0
	transport := srvClient.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, srvURL.Host)
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	handler.client = srvClient

	// 4. 构造流式推理请求，使得 isStreaming 条件满足
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	
	req := httptest.NewRequest(http.MethodPost, "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:streamGenerateContent", nil)
	req.Host = "generativelanguage.googleapis.com"
	req = req.WithContext(reqCtx)

	rec := httptest.NewRecorder()

	// 5. 模拟在 100ms 后主动取消客户端 Context，此时 Mock 服务端应该刚发出第 2 个 chunk
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancelReq()
	}()

	startTime := time.Now()
	// 执行 ProxyHandler。如果未优化，它会一直跑完 Mock 的 100 个 chunk（耗时约 5 秒）
	// 如果已优化，它会在 100ms 被 cancel 时立即退出
	handler.ServeHTTP(rec, req)
	duration := time.Since(startTime)

	t.Logf("Streaming request took %v", duration)

	// 6. 断言：应该在 100ms 到 500ms 内快速返回（允许调度抖动），而不是等 5 秒发完
	if duration >= 1500*time.Millisecond {
		t.Errorf("Streaming request was not cancelled promptly, took: %v", duration)
	}
	
	t.Logf("Test passed. Cancelled request terminated in %v", duration)
}
