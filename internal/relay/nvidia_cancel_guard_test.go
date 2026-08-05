package relay

// nvidia_cancel_guard_test.go 锁定 NVIDIA 换号循环「客户端取消守卫」的两个落点,回归
// 保护修复"客户端取消 → 整池被 context canceled 砍头 + 误拉黑 60s 冷却"的刷屏问题:
//
//   ① 循环首部 select<-r.Context().Done()(nvidia.go 头部守卫):入站请求在进入换号循环前
//      即已取消时,立即 return,不触碰任何账号(上游被请求数为 0),不写 502 兜底。
//   ② errDo != nil 分支特判 errors.Is(errDo, context.Canceled)(nvidia.go 失败分支):
//      客户端在上游 Do() 进行中取消时,errDo == context.Canceled,直接 return,
//      绝不把该号拉黑 60s 冷却,也不再换下一个号(否则会被同一已取消的 ctx 连续砍掉整个号池,
//      误拉黑全部可用号 → 紧接着的真实请求撞 nvidia_pool_empty)。
//
// 测试 ② 用进程内 cancelRT RoundTripper 模拟"Do 进行中被 ctx 取消":RoundTrip 阻塞在
// req.Context().Done(),测试侧 cancel 入站 ctx 后 RoundTrip 立即返回 context.Canceled,
// 完全不依赖真实上游网络/httptest.Server.Close,杜绝后台 goroutine(本地 VPN 探测)与
// 阻塞上游导致的 Close 死锁。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

// TestHandleNvidia_PreCancelGuard_StopLoopImmediately: 入站 ctx 在 handleNvidia
// 被调用前已取消(客户端发出请求后立即断开,或上游链路在选号前就告失败)。
// 期望循环首部 select 守卫立即命中并 return:
//   - 上游被请求数为 0(没把整个号池挨个试一遍);
//   - 所有账号 Cooldowns 为空(没误拉黑);
//   - 响应体不含 "nvidia pool exhausted" 502 兜底文案。
func TestHandleNvidia_PreCancelGuard_StopLoopImmediately(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		// 正常情况下不应被命中;若守卫失效被命中,快速返回 200 避免测试卡死。
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[]}`))
	}))
	defer upstream.Close()

	accs := []*account.Account{
		mkNvidiaAccount("nv-pre-1", "pre-1@pool", "k1", upstream.URL, "moonshotai/kimi-k2.5"),
		mkNvidiaAccount("nv-pre-2", "pre-2@pool", "k2", upstream.URL, "moonshotai/kimi-k2.5"),
		mkNvidiaAccount("nv-pre-3", "pre-3@pool", "k3", upstream.URL, "moonshotai/kimi-k2.5"),
	}
	handler, _, _, _ := newNvidiaTestHandler(t, accs)

	// 非流式 OpenAI Chat 入站,避免触发流式 streamClient/watchCancel 路径,聚焦换号循环守卫。
	body := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", strings.NewReader(body))
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	cancel() // 进入 handleNvidia 前就取消

	rr := httptest.NewRecorder()
	handler.handleNvidia(rr, req, &RelaySession{UserID: "u-pre", UserKey: "k-pre"})

	if upstreamCalls != 0 {
		t.Fatalf("预取消场景上游应 0 次命中(循环首部守卫未生效),实际命中 %d 次", upstreamCalls)
	}
	if strings.Contains(rr.Body.String(), "nvidia pool exhausted") {
		t.Errorf("预取消路径不应写出 502 兜底, got body: %s", rr.Body.String())
	}
	for _, id := range []string{"nv-pre-1", "nv-pre-2", "nv-pre-3"} {
		a := handler.accountMgr.GetAccountByID(id)
		if a == nil {
			t.Errorf("account %s 不存在", id)
			continue
		}
		if len(a.Cooldowns) > 0 {
			t.Errorf("account %s 取消后不应被拉黑冷却, got Cooldowns=%v", id, a.Cooldowns)
		}
	}
}

// cancelRT 是一个进程内 RoundTripper,用于精准模拟"上游 Do 进行中被客户端 ctx 取消":
//   - RoundTrip 命中后把 req 投到 hit chan(供测试断言"请求确实发出了"),再阻塞在
//     req.Context().Done() 上;
//   - 测试侧 cancel 入站 ctx → 上游 req 的 ctx(由 http.NewRequestWithContext(r.Context(),...)
//     绑定)即被撤销 → RoundTrip 解阻塞 → 返回 req.Context().Err() = context.Canceled;
//   - 完全不发起真实网络/TLS,杜绝 httptest.Server.Close 因后台 goroutine(本地 VPN 探测)
//     与阻塞上游连接而死的死锁;RoundTrip 在 ctx 取消后即退出,无 goroutine 泄漏。
type cancelRT struct {
	hit chan *http.Request
}

func (c *cancelRT) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case c.hit <- req:
	default:
	}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestHandleNvidia_InFlightCancelNoCooldown: 客户端在上游 Do() 进行中(首号已发出、上游还在处理)
// 主动取消。期望 errDo==context.Canceled 命中 nvidia.go 失败分支特判,直接 return:
//   - 上游 RoundTrip 仅被命中 1 次(不来回换号砍掉整个池);
//   - 被中断的首号与其余号均无 60s 冷却(取消是客户端行为,非账号故障);
//   - 无 502 兜底写出。
func TestHandleNvidia_InFlightCancelNoCooldown(t *testing.T) {
	// 用 cancelRT 替换非流式 client 的 transport;账号 BaseURL 设为占位 URL(不会被真实拨号,
	// 因 RoundTrip 在 Dial 之前就拦截返回)。这样测试既不依赖真实上游,也不需要 Close 任何服务器。
	hit := make(chan *http.Request, 1)

	accs := []*account.Account{
		mkNvidiaAccount("nv-if-1", "if-1@pool", "k1", "http://nvidia-upstream.example/v1", "moonshotai/kimi-k2.5"),
		mkNvidiaAccount("nv-if-2", "if-2@pool", "k2", "http://nvidia-upstream.example/v1", "moonshotai/kimi-k2.5"),
		mkNvidiaAccount("nv-if-3", "if-3@pool", "k3", "http://nvidia-upstream.example/v1", "moonshotai/kimi-k2.5"),
	}
	handler, _, _, _ := newNvidiaTestHandler(t, accs)
	// 关键:用 cancelRT 接管非流式上游出站,使 Do() 在 RoundTrip 阶段被 ctx 取消以
	// errors.Is(errDo, context.Canceled)==true 返回,精准命中 nvidia.go 失败分支特判。
	handler.client = &http.Client{Transport: &cancelRT{hit: hit}, Timeout: 0}

	body := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/nvidia/v1/chat/completions", strings.NewReader(body))
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.handleNvidia(rr, req, &RelaySession{UserID: "u-if", UserKey: "k-if"})
	}()

	// 等首个账号的上游请求确实发出(RoundTrip 命中),确认处于 "Do 进行中" 状态。
	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("上游 RoundTrip 未在超时内被首个账号命中(请求未发出或选号失败)")
	}
	// 触发 ctx 取消(模拟客户端主动断开):RoundTrip 解阻塞返回 context.Canceled。
	cancel()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatalf("handleNvidia 未在 ctx 取消后退出(errDo 特判未生效)")
	}

	// 取消后应已退出;hit 缓冲至多 1(后续不再换号试满池,因特判分支直接 return)。
	if got := len(hit); got > 1 {
		t.Errorf("预期取消后不再换号,上游 RoundTrip 被命中 %d 次(应仅首号 1 次)", got)
	}
	if strings.Contains(rr.Body.String(), "nvidia pool exhausted") {
		t.Errorf("取消路径不应写出 502 兜底, got body: %s", rr.Body.String())
	}
	for _, id := range []string{"nv-if-1", "nv-if-2", "nv-if-3"} {
		a := handler.accountMgr.GetAccountByID(id)
		if a == nil {
			t.Errorf("account %s 不存在", id)
			continue
		}
		if len(a.Cooldowns) > 0 {
			t.Errorf("account %s 不应在客户端取消后被拉黑 60s 冷却, got Cooldowns=%v", id, a.Cooldowns)
		}
	}
}
