package relay

// nvidia_hedge_test.go: 对冲竞速原语 hedgedUpstreamDo 的确定性时序验证。
// 全程假 RoundTripper 以 channel/定时器控制「响应头到达时刻」,不触真实网络;
// 覆盖:阈值内不对冲、对冲胜[F1]掐断败方主 ctx+晚到响应由收割关 Body、主胜+对冲 ctx 取消、
// 主先错立即败(不救场——差错不构成胜负)、双方皆错优先主错误、无候选静默退化、客户端取消穿透、
// 多对冲同时轰出(次对冲胜+全部败方收割)、候选耗尽自动降级、单边形态(maxParallel=1)、
// [F1] 反向:主胜绝不掐主派生 ctx(流式正文生死随 parent)。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"antigravity-proxy/internal/account"
)

type hedgeRTFunc func(*http.Request) (*http.Response, error)

func (f hedgeRTFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// hedgeTrackBody 记录 Close 是否发生(收割协程关败方响应体的探针)。幂等 Close 防双 close panic。
type hedgeTrackBody struct {
	closed chan struct{}
	once   sync.Once
}

func newHedgeTrackBody() *hedgeTrackBody             { return &hedgeTrackBody{closed: make(chan struct{})} }
func (b *hedgeTrackBody) Read(p []byte) (int, error) { return 0, io.EOF }
func (b *hedgeTrackBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func hedgeOKResp(body *hedgeTrackBody) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header:     make(http.Header),
		Body:       body,
	}
}

func hedgeTestReq(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("建测试请求失败: %v", err)
	}
	return req
}

// hedgeBuilder 造「打了标」的对冲请求(transport 以 X-Test-Role: hedge-<idx> 区分各分支)。
func hedgeBuilder(t *testing.T, counter *int32) func(context.Context, int) (*http.Request, error) {
	t.Helper()
	return func(ctx context.Context, hedgeIdx int) (*http.Request, error) {
		if counter != nil {
			atomic.AddInt32(counter, 1)
		}
		req := hedgeTestReq(t, ctx)
		req.Header.Set("X-Test-Role", fmt.Sprintf("hedge-%d", hedgeIdx))
		return req, nil
	}
}

// TestHedgedUpstreamDo_PrimaryFastNoHedge 锁定:主请求阈值内返回 → 永不对冲(零开销路径)。
func TestHedgedUpstreamDo_PrimaryFastNoHedge(t *testing.T) {
	body := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		return hedgeOKResp(body), nil
	})}
	var built int32
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 50*time.Millisecond, false, 3, hedgeBuilder(t, &built))
	if res.err != nil || res.resp == nil {
		t.Fatalf("主请求应立即成功,实际 err=%v resp=%v", res.err, res.resp)
	}
	if res.hedgeFired || res.hedgeWon {
		t.Fatalf("阈值内返回不应触发对冲: fired=%v won=%v", res.hedgeFired, res.hedgeWon)
	}
	if atomic.LoadInt32(&built) != 0 {
		t.Fatalf("buildHedge 不应被调用,实际 %d 次", built)
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_HedgeWins 锁定 [F1]:对冲胜 → 败方主请求派生 ctx 立即掐断;
// 其响应头若恰在掐断瞬间竟态送达(真实网络可能发生),Body 由收割协程关闭(防泄漏);
// 胜方 Body 归调用方,收割协程不得触碰。
func TestHedgedUpstreamDo_HedgeWins(t *testing.T) {
	primaryGate := make(chan struct{})
	primaryCanceled := make(chan struct{})
	primaryBody := newHedgeTrackBody()
	hedgeBody := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			return hedgeOKResp(hedgeBody), nil
		}
		// ctx 掐断探针 + 门闸放行后才交回「竟态晚到」的响应头(无视取消,模拟头已到手)
		go func() {
			<-r.Context().Done()
			close(primaryCanceled)
		}()
		<-primaryGate
		return hedgeOKResp(primaryBody), nil
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 2, hedgeBuilder(t, nil))
	if res.err != nil || res.resp == nil {
		t.Fatalf("对冲应胜出,实际 err=%v", res.err)
	}
	if !res.hedgeFired || !res.hedgeWon || res.winnerHedgeIdx != 1 {
		t.Fatalf("应标记对冲已发且胜(分支1): fired=%v won=%v idx=%d", res.hedgeFired, res.hedgeWon, res.winnerHedgeIdx)
	}
	// 胜方 Body 归调用方,收割协程不得触碰
	select {
	case <-hedgeBody.closed:
		t.Fatal("胜方(对冲)Body 被收割协程误关")
	default:
	}
	// [F1] 对冲胜瞬间败方主请求派生 ctx 必须被掐断(僵尸请求断流)
	select {
	case <-primaryCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("对冲胜后败方主请求 ctx 未被掐断(僵尸请求烧算力)")
	}
	// 竟态晚到的败方响应头由收割协程关 Body
	close(primaryGate)
	select {
	case <-primaryBody.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("败方主请求响应体未被收割关闭(泄漏)")
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_PrimaryWinsHedgeCanceled 锁定:对冲已触发但主先回响应头 → 主胜,
// 全部对冲派生 ctx 被立即取消(transport 观测到 context.Canceled)。
func TestHedgedUpstreamDo_PrimaryWinsHedgeCanceled(t *testing.T) {
	primaryBody := newHedgeTrackBody()
	hedgeCtxErr := make(chan error, 2)
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			<-r.Context().Done()
			hedgeCtxErr <- r.Context().Err()
			return nil, r.Context().Err()
		}
		timer := time.NewTimer(60 * time.Millisecond) // 主 60ms 回 > 20ms 阈值 → 对冲已发
		defer timer.Stop()
		select {
		case <-timer.C:
			return hedgeOKResp(primaryBody), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 3, hedgeBuilder(t, nil))
	if !res.hedgeFired || res.hedgeWon {
		t.Fatalf("应对冲已发但主胜: fired=%v won=%v", res.hedgeFired, res.hedgeWon)
	}
	if res.err != nil || res.resp == nil {
		t.Fatalf("主胜应返回主响应,实际 err=%v", res.err)
	}
	// 两个对冲分支的 ctx 都应被取消
	for i := 0; i < 2; i++ {
		select {
		case err := <-hedgeCtxErr:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("对冲取消应为 context.Canceled,实际 %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("主胜后对冲 ctx 未被取消")
		}
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_PrimaryErrorBeforeTimer 锁定:主请求在阈值前出错 → 立即败,
// 不触发对冲 —— 「差错不构成对冲扳机」是计费安全红线(对冲唯一扳机是 delay 计时器)。
func TestHedgedUpstreamDo_PrimaryErrorBeforeTimer(t *testing.T) {
	sentinel := errors.New("primary dial boom")
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		return nil, sentinel
	})}
	var built int32
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 5*time.Second, false, 3, hedgeBuilder(t, &built))
	if res.resp != nil {
		t.Fatal("主错不应有响应")
	}
	if !errors.Is(res.err, sentinel) {
		t.Fatalf("错误应原样回传主错误(调用方据此走既有冷却链),实际 %v", res.err)
	}
	if res.hedgeFired || atomic.LoadInt32(&built) != 0 {
		t.Fatalf("主阈值前出错不得触发对冲: fired=%v built=%d", res.hedgeFired, built)
	}
}

// TestHedgedUpstreamDo_BothErrorPreferPrimary 锁定:对冲触发后双方皆错 → 回主错误
// (保持错误/冷却链口径与未开对冲时一致)。
func TestHedgedUpstreamDo_BothErrorPreferPrimary(t *testing.T) {
	sentinelPrimary := errors.New("primary timeout")
	sentinelHedge := errors.New("hedge refused")
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			return nil, sentinelHedge
		}
		timer := time.NewTimer(60 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil, sentinelPrimary
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 2, hedgeBuilder(t, nil))
	if res.resp != nil {
		t.Fatal("双方皆错不应有响应")
	}
	if !res.hedgeFired {
		t.Fatal("对冲应已触发")
	}
	if !errors.Is(res.err, sentinelPrimary) {
		t.Fatalf("双方皆错应优先回主错误,实际 %v", res.err)
	}
}

// TestHedgedUpstreamDo_NoCandidateDegrades 锁定:无对冲候选 → 静默退化为裸 Do,
// 主请求照常返回(零行为变化是号池仅剩主号场景的硬性要求)。
func TestHedgedUpstreamDo_NoCandidateDegrades(t *testing.T) {
	body := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		timer := time.NewTimer(60 * time.Millisecond) // 阈值后才回,验证「未对冲但仍在等」
		defer timer.Stop()
		select {
		case <-timer.C:
			return hedgeOKResp(body), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 4,
		func(context.Context, int) (*http.Request, error) { return nil, errNoHedgeCandidate })
	if res.err != nil || res.resp == nil {
		t.Fatalf("无候选应退化为裸 Do 并照常返回,实际 err=%v", res.err)
	}
	if res.hedgeFired || res.hedgeWon {
		t.Fatalf("无候选不得标记对冲: fired=%v won=%v", res.hedgeFired, res.hedgeWon)
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_ClientCancelPropagates 锁定:客户端断开(parent 取消)时
// 全部分支皆死,Err 为 context.Canceled 家族 —— 与裸 Do 口径一致,调用方取消特判不受影响。
func TestHedgedUpstreamDo_ClientCancelPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	res := hedgedUpstreamDo(parent, client, hedgeTestReq(t, parent), 20*time.Millisecond, false, 3, hedgeBuilder(t, nil))
	if res.resp != nil {
		t.Fatal("客户端取消不应有响应")
	}
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("客户端取消应回 context.Canceled 家族错误,实际 %v", res.err)
	}
}

// TestHedgedUpstreamDo_MultiHedgeSecondWins 锁定 maxParallel=3 同时轰出:主与对冲1挂死,
// 对冲2先回响应头 → winnerHedgeIdx=2;[F1] 败方主请求派生 ctx 掐断、败方对冲1被取消,
// 主响应头竟态晚到时由收割协程关 Body;胜方 Body 不得触碰。
func TestHedgedUpstreamDo_MultiHedgeSecondWins(t *testing.T) {
	primaryBody := newHedgeTrackBody()
	primaryGate := make(chan struct{})
	primaryCanceled := make(chan struct{})
	hedge1Canceled := make(chan struct{})
	hedge2Body := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Header.Get("X-Test-Role") {
		case "hedge-2":
			return hedgeOKResp(hedge2Body), nil // 对冲2 立即回响应头
		case "hedge-1":
			select {
			case <-r.Context().Done():
				close(hedge1Canceled)
				return nil, r.Context().Err()
			case <-time.After(3 * time.Second):
				return nil, errors.New("hedge-1 should have been canceled")
			}
		default: // 主请求:ctx 掐断探针 + 门闸后交回「竟态晚到」响应头
			go func() {
				<-r.Context().Done()
				close(primaryCanceled)
			}()
			<-primaryGate
			return hedgeOKResp(primaryBody), nil
		}
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 3, hedgeBuilder(t, nil))
	if !res.hedgeFired || !res.hedgeWon || res.winnerHedgeIdx != 2 {
		t.Fatalf("应对冲2胜: fired=%v won=%v idx=%d err=%v", res.hedgeFired, res.hedgeWon, res.winnerHedgeIdx, res.err)
	}
	// 败方对冲1应被 cancel
	select {
	case <-hedge1Canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("败方对冲1 ctx 未被取消")
	}
	// [F1] 败方主请求派生 ctx 应被掐断
	select {
	case <-primaryCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("对冲胜后败方主请求 ctx 未被掐断(僵尸请求烧算力)")
	}
	// 胜者 Body 不得被收割
	select {
	case <-hedge2Body.closed:
		t.Fatal("胜方对冲2 Body 被误关")
	default:
	}
	// 竟态晚到的主响应头由收割协程关 Body
	close(primaryGate)
	select {
	case <-primaryBody.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("败方主请求响应体未被收割关闭(泄漏)")
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_PartialCandidates 锁定:候选不足时按实际可发数降级
// (build 只对 hedgeIdx=1 成功,其余返回 errNoHedgeCandidate),胜者/[F1]掐断/收割语义不变。
func TestHedgedUpstreamDo_PartialCandidates(t *testing.T) {
	body := newHedgeTrackBody()
	primaryGate := make(chan struct{})
	primaryCanceled := make(chan struct{})
	primaryBody := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			return hedgeOKResp(body), nil
		}
		go func() {
			<-r.Context().Done()
			close(primaryCanceled)
		}()
		<-primaryGate
		return hedgeOKResp(primaryBody), nil
	})}
	var buildCalls int32
	build := func(ctx context.Context, hedgeIdx int) (*http.Request, error) {
		atomic.AddInt32(&buildCalls, 1)
		if hedgeIdx > 1 {
			return nil, errNoHedgeCandidate // 候选池只够 1 个对冲号
		}
		req := hedgeTestReq(t, ctx)
		req.Header.Set("X-Test-Role", fmt.Sprintf("hedge-%d", hedgeIdx))
		return req, nil
	}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 5, build)
	if atomic.LoadInt32(&buildCalls) != 4 {
		t.Fatalf("maxParallel=5 应尝试构造 4 个对冲,实际 %d", buildCalls)
	}
	if !res.hedgeFired || !res.hedgeWon || res.winnerHedgeIdx != 1 {
		t.Fatalf("唯一的对冲分支应胜: fired=%v won=%v idx=%d err=%v", res.hedgeFired, res.hedgeWon, res.winnerHedgeIdx, res.err)
	}
	// [F1] 败方主请求派生 ctx 应被掐断
	select {
	case <-primaryCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("对冲胜后败方主请求 ctx 未被掐断(僵尸请求烧算力)")
	}
	// 胜者 Body 归调用方,收割协程不得触碰
	select {
	case <-body.closed:
		t.Fatal("胜方对冲 Body 被收割协程误关")
	default:
	}
	// 竟态晚到的主响应头由收割协程关 Body
	close(primaryGate)
	select {
	case <-primaryBody.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("败方主请求响应体未被收割关闭(泄漏)")
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_MaxParallelOneDegrades 锁定 maxParallel=1 单边形态:无任何对冲逻辑,
// 等价裸 Do(timer 与 buildHedge 完全不参与)。
func TestHedgedUpstreamDo_MaxParallelOneDegrades(t *testing.T) {
	body := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		timer := time.NewTimer(30 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return hedgeOKResp(body), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	var built int32
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 5*time.Millisecond, false, 1, hedgeBuilder(t, &built))
	if res.err != nil || res.resp == nil {
		t.Fatalf("单边形态应照常返回,实际 err=%v", res.err)
	}
	if res.hedgeFired || res.hedgeWon || atomic.LoadInt32(&built) != 0 {
		t.Fatalf("maxParallel=1 不得触发任何对冲: fired=%v won=%v built=%d", res.hedgeFired, res.hedgeWon, built)
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_HedgeWinCancelsPrimaryCtx 锁定 [F1]:对冲胜 → 败方主请求的派生 ctx
// 被立即掐断(运输层观测到 context.Canceled),不白烧僵尸请求的剩余上游算力。
func TestHedgedUpstreamDo_HedgeWinCancelsPrimaryCtx(t *testing.T) {
	primaryCanceled := make(chan struct{})
	hedgeBody := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			return hedgeOKResp(hedgeBody), nil
		}
		<-r.Context().Done()
		close(primaryCanceled)
		return nil, r.Context().Err()
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 2, hedgeBuilder(t, nil))
	if !res.hedgeWon {
		t.Fatalf("应对冲胜: won=%v err=%v", res.hedgeWon, res.err)
	}
	select {
	case <-primaryCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("对冲胜后败方主请求 ctx 未被掐断(僵尸请求烧算力)")
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_PrimaryWinKeepsPrimaryCtxAlive 锁定 [F1] 反向:主胜时主请求的派生
// ctx 绝不被掐断 —— 响应流式生命周期延伸到 hedgedUpstreamDo 之外,误掐即截断正文。
func TestHedgedUpstreamDo_PrimaryWinKeepsPrimaryCtxAlive(t *testing.T) {
	primaryBody := newHedgeTrackBody()
	var primaryCtxSeen context.Context
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			<-r.Context().Done() // 对冲败方:等自己被取消
			return nil, r.Context().Err()
		}
		primaryCtxSeen = r.Context()
		timer := time.NewTimer(60 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return hedgeOKResp(primaryBody), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 20*time.Millisecond, false, 2, hedgeBuilder(t, nil))
	if res.hedgeWon || res.err != nil || res.resp == nil {
		t.Fatalf("应主胜: won=%v err=%v", res.hedgeWon, res.err)
	}
	// 胜者判定后再观察:主流式期间主 ctx 必须仍然存活
	time.Sleep(100 * time.Millisecond)
	if primaryCtxSeen == nil || primaryCtxSeen.Err() != nil {
		t.Fatalf("主胜后主请求 ctx 被误掐: ctx=%v err=%v", primaryCtxSeen, primaryCtxSeen.Err())
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_ImmediateHedgeWinsWithoutTimer 锁定:即刻模式下定时器被完全旁路
// (delay 设为 60s 不可得值),主与对冲同刻出发,对冲照样可胜,败方主 ctx 被掐断。
func TestHedgedUpstreamDo_ImmediateHedgeWinsWithoutTimer(t *testing.T) {
	hedgeBody := newHedgeTrackBody()
	primaryKilled := make(chan struct{})
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			return hedgeOKResp(hedgeBody), nil
		}
		<-r.Context().Done()
		close(primaryKilled)
		return nil, r.Context().Err()
	})}
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 60*time.Second, true, 2, hedgeBuilder(t, nil))
	if res.err != nil || res.resp == nil {
		t.Fatalf("即刻模式应对冲胜,实际 err=%v", res.err)
	}
	if !res.hedgeFired || !res.hedgeWon || res.winnerHedgeIdx != 1 {
		t.Fatalf("即刻模式对冲应立即胜(定时器旁路): fired=%v won=%v idx=%d", res.hedgeFired, res.hedgeWon, res.winnerHedgeIdx)
	}
	select {
	case <-primaryKilled:
	case <-time.After(2 * time.Second):
		t.Fatal("即刻对冲胜后主派生 ctx 未被掐断")
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_ImmediatePrimaryWinsHedgesCanceled 锁定:即刻模式下主仍可最快,
// 主胜后全部对冲派生 ctx 被掐断(竞速纪律与延迟模式完全一致)。
func TestHedgedUpstreamDo_ImmediatePrimaryWinsHedgesCanceled(t *testing.T) {
	primaryBody := newHedgeTrackBody()
	hedgeKilled := make(chan struct{}, 2)
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.Header.Get("X-Test-Role"), "hedge-") {
			<-r.Context().Done()
			hedgeKilled <- struct{}{}
			return nil, r.Context().Err()
		}
		return hedgeOKResp(primaryBody), nil // 主立即回响应头
	})}
	var built int32
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 60*time.Second, true, 3, hedgeBuilder(t, &built))
	if res.hedgeWon || res.err != nil || res.resp == nil {
		t.Fatalf("即刻模式主应先胜: won=%v err=%v", res.hedgeWon, res.err)
	}
	if atomic.LoadInt32(&built) != 2 {
		t.Fatalf("即刻模式主+2对冲应同刻出发(built=2),实际 %d", built)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-hedgeKilled:
		case <-time.After(2 * time.Second):
			t.Fatal("即刻模式主胜后对冲派生 ctx 未被掐断")
		}
	}
	res.resp.Body.Close()
}

// TestHedgedUpstreamDo_ImmediateMaxParallelOneDegrades 锁定:即刻+单边形态 = 逐字裸 Do,
// 对冲构建器绝不被调用。
func TestHedgedUpstreamDo_ImmediateMaxParallelOneDegrades(t *testing.T) {
	body := newHedgeTrackBody()
	client := &http.Client{Transport: hedgeRTFunc(func(r *http.Request) (*http.Response, error) {
		return hedgeOKResp(body), nil
	})}
	var built int32
	res := hedgedUpstreamDo(context.Background(), client, hedgeTestReq(t, context.Background()), 60*time.Second, true, 1, hedgeBuilder(t, &built))
	if res.err != nil || res.resp == nil {
		t.Fatalf("即刻单边形态应照常返回,实际 err=%v", res.err)
	}
	if res.hedgeFired || res.hedgeWon || atomic.LoadInt32(&built) != 0 {
		t.Fatalf("即刻+maxParallel=1 不得触发任何对冲: fired=%v won=%v built=%d", res.hedgeFired, res.hedgeWon, built)
	}
	res.resp.Body.Close()
}

// TestFormatNvidiaHedgeRace 锁定裁决日志参赛名单渲染:分支序号升序稳定(与点火顺序/裁决
// 日志里的 winnerHedgeIdx 一一对应)、空对冲防守、nil 账号不 panic。
func TestFormatNvidiaHedgeRace(t *testing.T) {
	acc := func(email string) *account.Account { return &account.Account{Email: email} }
	tests := []struct {
		name    string
		primary string
		hedges  map[int]*account.Account
		want    string
	}{
		{"空对冲防守", "a@x.dev", map[int]*account.Account{}, "主[a@x.dev]"},
		{"单对冲", "a@x.dev", map[int]*account.Account{1: acc("b@x.dev")}, "主[a@x.dev]+对冲1[b@x.dev]"},
		{"多对冲乱序插入按分支序号升序", "a@x.dev",
			map[int]*account.Account{3: acc("d@x.dev"), 1: acc("b@x.dev"), 2: acc("c@x.dev")},
			"主[a@x.dev]+对冲1[b@x.dev]+对冲2[c@x.dev]+对冲3[d@x.dev]"},
		{"nil 账号防御", "a@x.dev", map[int]*account.Account{1: nil}, "主[a@x.dev]+对冲1[<nil>]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatNvidiaHedgeRace(tt.primary, tt.hedges); got != tt.want {
				t.Fatalf("名单渲染不符:\n got=%s\nwant=%s", got, tt.want)
			}
		})
	}
}
