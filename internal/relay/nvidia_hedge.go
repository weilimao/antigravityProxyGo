package relay

// nvidia_hedge.go: NVIDIA 号池「对冲请求」(hedged request) 竞速原语 + 配置 Safe 访问器。
//
// 背景结论(会话内实测):NIM 上游首帧延迟 = 「GPU 副本排队抽签」×「前缀缓存命中与否」
// 的叠加彩票(0% 缓存 ~300K 上下文首帧 20-38s,96%+ 命中 4-7s;同账户同模型逐请求波动)。
// 主请求一旦被分到慢队列,本地任何传输层优化都救不回来 —— 对冲是唯一的本地杠杆:
// 主请求发出 delayMs 仍无响应头时,用池内其他账号同时轰出 maxParallel-1 份相同请求
// (各账号大概率落不同副本队列),谁先回响应头用谁,败方立即取消。
//
// 设计红线(密集评审结论,勿改):
//  1. 仅「每账号轮换的首次 Do」启用(调用方以 singleAttempt==1 把守):429 原地重试、
//     断流蓄流回放、兜底代理轮一律不对冲 —— 避免对冲放大重试,计费失控。
//  2. 差错不构成胜负:单分支 transport 错误只淘汰自己,其余分支继续等;全部皆错才败。
//     因此「主请求提前失败」不会提前触发对冲(保持既有冷却/换号链语义逐字不变),
//     对冲仅由配置扳机触发:delay 计时器(默认)或即刻模式(开局即全员竞速)。
//  3. 败方不计故障、不冷却、不解粘性绑定 —— 对冲未成功不代表账号坏。
//  4. 竞速形态(maxParallel≥2)下所有分支(含主请求)都挂在派生可取消 ctx 上:对冲胜时
//     立即掐断败方主请求(否则它会在上游继续跑完整 prefill/decode 白烧全程算力);主胜时
//     绝不 cancel 主派生 ctx —— 响应流式生命周期延伸到本函数外,其生死随 parent 自然终结。
//     单边形态(maxParallel=1)不派生任何 ctx,逐字等价裸 Do(vet lostcancel 亦据此静默)。
//  5. 对冲候选不足(号池可用账号少于目标并发)时自动降级为实际可用数,
//     完全不候选则静默退化为裸 Do,零行为变化。
//  6. 最坏上游计费 = maxParallel 倍(全部败方预填算力浪费)——调用方侧 settings
//     层钳位 [2,5],本原语只管竞速正确性。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"antigravity-proxy/internal/account"
)

// errNoHedgeCandidate 表示无可用对冲账号(如号池仅剩主号或对冲号已被耗尽)。
// buildHedge 返回它时该分支静默不启,竞速退化为已发分支等待,调用方不感知、不记录。
var errNoHedgeCandidate = errors.New("nvidia hedge: no candidate account")

// hedgeDoOutcome 是单侧分支的最终结局(idx: 0=主请求,1..N=对冲;拿到响应头 err==nil,
// 或 transport 出错)。所有分支投递到同一条缓冲通道,容量 ≥ 分支数,发送永不阻塞。
type hedgeDoOutcome struct {
	idx  int
	resp *http.Response
	err  error
}

// hedgeResult 是竞速的对外裁决。
type hedgeResult struct {
	resp           *http.Response // 胜方响应(全部皆错时为 nil)
	err            error          // 全部皆错时的错误(优先主请求错误,保持既有日志/冷却语义)
	hedgeFired     bool           // 是否实际发出过至少一个对冲请求
	hedgeWon       bool           // 胜方是否为对冲请求
	winnerHedgeIdx int            // 胜方对冲分支序号(1..maxParallel-1;仅 hedgeWon 时有意义)
}

// hedgedUpstreamDo 竞速执行主请求与 0..N 个对冲请求,返回胜方。
//
// 参数:
//   - parent:      客户端请求 ctx(通常 r.Context()),对冲请求由此派生;parent 一旦取消
//     (客户端断开),全部分支都会因 ctx 传播而错,Err 将是 context.Canceled 家族 —
//     与裸 Do 在客户端断开时的返回口径完全一致,调用方既有特判不受影响。
//   - primary:     调用方已构造好的主请求(其 ctx 绑定由调用方负责,本函数不改动)。
//   - delay:       触发对冲的等待阈值(仅延迟模式生效);主请求在此之前返回(无论成败)→ 永不对冲。
//   - immediate:   即刻竞赛模式开关。true 时放弃定时器,主请求与全部对冲同刻出发
//     (每次请求上游计费恒为 maxParallel 倍,用于极致首帧场景)。
//   - maxParallel: 总参赛请求数(含主请求),<=1 时直接退化为单边等待主请求。
//     触发时最多补发 maxParallel-1 个对冲;buildHedge 返回错误即少发一个
//     (候选耗尽自动降级),绝不为凑数而复用同一账号。
//   - buildHedge:  惰性构造第 hedgeIdx 个对冲请求(择号/占并发槽/建请求体副本),
//     hedgeIdx 从 1 开始递增;于触发时刻在本 goroutine 同步调用 —— 因此其闭包
//     捕获的调用方变量无数据竞争。返回错误(含 errNoHedgeCandidate)即跳过该分支。
//
// 资源纪律:败方若已拿到响应(纳秒级同帧完成),收割协程负责关闭其 Body;
// 败方仍在排队则 cancel 其派生 ctx(主侧同样持有派生 cancel,对冲胜即掐断——
// 这是刻意的:主败方若不掐,会在上游继续跑完整 prefill/decode,白烧全程算力)。
// 收割协程按「已发分支数-1」精确沥干共享通道后退出,不残留常驻协程。
func hedgedUpstreamDo(parent context.Context, client *http.Client, primary *http.Request, delay time.Duration, immediate bool, maxParallel int, buildHedge func(ctx context.Context, hedgeIdx int) (*http.Request, error)) hedgeResult {
	if maxParallel < 1 {
		maxParallel = 1
	}
	outcomes := make(chan hedgeDoOutcome, maxParallel)

	launch := func(idx int, req *http.Request) {
		go func() {
			resp, err := client.Do(req)
			outcomes <- hedgeDoOutcome{idx: idx, resp: resp, err: err}
		}()
	}

	hedgeTotal := maxParallel - 1
	if hedgeTotal == 0 {
		// 单边形态:不派生可取消 ctx、无计时器、无对冲,逐字等价裸 Do。
		launch(0, primary)
		out := <-outcomes
		return hedgeResult{resp: out.resp, err: out.err}
	}

	// [F1] 竞速形态下主请求改挂派生可取消 ctx:对冲胜时立即掐断败方主请求(僵尸请求在上游
	// 继续跑会白烧全程 prefill 算力);主胜时绝不 cancel —— 其响应流式延伸到本函数外,
	// 派生根仍是 parent(r.Context()),客户端断开传播不变。
	primaryCtx, primaryCancel := context.WithCancel(parent)
	primary = primary.WithContext(primaryCtx)
	launch(0, primary)

	// 以下变量仅被本 goroutine(select 循环)读写,buildHedge 亦在本 goroutine 同步执行,
	// 分支协程只往 outcomes 发送,不与本区共享变量。
	// hedgeCancels 改为 map[分支序号]cancel:胜方裁决时只能取消「败方」,绝不能顺手取消掉
	// 胜者自己的派生 ctx —— 胜方响应体读取还在运行,自己 cancel 自己 = 自断流。
	// (旧 []slice 版本在"对冲胜"路径会连带取消胜方,触发下游 ReadAll：context canceled → 502。
	//  新测试 TestHandleNvidia_HedgeTriggerLog 首次把这条回归显化,工程上必须戒掉。)
	hedgeCancels := make(map[int]context.CancelFunc)
	firedHedges := 0

	// fireAll 触发时同时轰出全部对冲;候选耗尽的分支被 buildHedge 静默跳过。
	fireAll := func() {
		for i := 1; i <= hedgeTotal; i++ {
			hctx, cancel := context.WithCancel(parent)
			req, err := buildHedge(hctx, i)
			if err != nil {
				cancel()
				continue
			}
			hedgeCancels[i] = cancel
			firedHedges++
			launch(i, req)
		}
	}

	// reap 收割全部败方结局:关闭已到达响应的 Body(防连接悬挂泄漏)。
	// 已发分支总数 launched 各投递恰好一次,读完 launched-1 个即退出。
	reap := func(launched int) {
		go func() {
			for j := 0; j < launched-1; j++ {
				out := <-outcomes
				if out.resp != nil {
					out.resp.Body.Close()
				}
			}
		}()
	}

	// 点火:延迟模式挂定时器;即刻模式此刻于本 goroutine 同步轰出全部对冲,
	// 定时器通道置 nil 使 select 的 timer 分支永久休眠(副作用纪律与定时器路径一致)。
	var timerC <-chan time.Time
	if immediate {
		fireAll()
	} else {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		timerC = timer.C
	}

	dead := 0            // 已终结分支数(含主)
	var primaryErr error // 主分支错误暂存(皆败时优先回填,保持错误/冷却链口径)

	for {
		select {
		case out := <-outcomes:
			if out.err == nil && out.resp != nil {
				// 首个响应头到达者胜:只取消「败方」对冲派生 ctx(跳过胜方),
				// 收割其余已发分支(自然终结或已被 cancel 的分支,结局都会被收割读取)。
				for idx, c := range hedgeCancels {
					if idx != out.idx {
						c()
					}
				}
				if out.idx != 0 {
					primaryCancel() // [F1] 僵尸主请求立即断流,不烧剩余算力
				} else {
					// 主胜:流式正文生命周期延伸到本函数之外,cancel 所有权移交 parent 终结时点;
					// KeepAlive 显式声明这是刻意的生命周期移交而非泄漏(lostcancel 据此静默)。
					runtime.KeepAlive(primaryCancel)
				}
				reap(1 + firedHedges)
				return hedgeResult{
					resp:           out.resp,
					hedgeFired:     firedHedges > 0,
					hedgeWon:       out.idx > 0,
					winnerHedgeIdx: out.idx,
				}
			}
			// 差错只淘汰本分支
			if out.idx == 0 {
				primaryErr = out.err
				if firedHedges == 0 {
					// 计时器未触发时主先死 = 裸 Do 错误语义,立即败(既有冷却/换号链零变化)。
					primaryCancel()
					return hedgeResult{err: primaryErr}
				}
			}
			dead++
			if dead == 1+firedHedges {
				// 全部已发分支皆错。timer 若在飞则对冲尚未发(候选存在与否未知),
				// 此时主已死 → 同裸 Do 错误语义直返,不再补发(差错不做对冲扳机)。
				primaryCancel()
				if primaryErr != nil {
					return hedgeResult{err: primaryErr, hedgeFired: firedHedges > 0}
				}
				return hedgeResult{err: out.err, hedgeFired: firedHedges > 0}
			}
		case <-timerC:
			fireAll()
		}
	}
}

// ============ Handler 层配置 Safe 访问器(镜像 nvidia.go 既有 recover 口径) ============

// isNvidiaHedgeEnabledSafe 读取对冲开关;settingsMgr 未注入/panic 时回退 false(关闭)。
func (h *APICompatHandler) isNvidiaHedgeEnabledSafe() (enabled bool) {
	if h == nil || h.settingsMgr == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return h.settingsMgr.IsNvidiaHedgeEnabled()
}

// getNvidiaHedgeDelayMsSafe 读取对冲触发延迟(毫秒);异常时回退 0(调用方据此放弃对冲)。
func (h *APICompatHandler) getNvidiaHedgeDelayMsSafe() (ms int) {
	if h == nil || h.settingsMgr == nil {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			ms = 0
		}
	}()
	return h.settingsMgr.GetNvidiaHedgeDelayMs()
}

// getNvidiaHedgeMaxParallelSafe 读取对冲总并发(含主请求);异常时回退 0(调用方按 <2 兜底 2)。
func (h *APICompatHandler) getNvidiaHedgeMaxParallelSafe() (n int) {
	if h == nil || h.settingsMgr == nil {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			n = 0
		}
	}()
	return h.settingsMgr.GetNvidiaHedgeMaxParallel()
}

// isNvidiaHedgeImmediateSafe 读取即刻竞赛开关;settingsMgr 未注入/panic 时回退 false(延迟模式)。
func (h *APICompatHandler) isNvidiaHedgeImmediateSafe() (enabled bool) {
	if h == nil || h.settingsMgr == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return h.settingsMgr.IsNvidiaHedgeImmediate()
}

// ============ 裁决日志渲染(纯函数,可单测) ============

// formatNvidiaHedgeRace 渲染竞速参赛名单:主号 + 各对冲分支账号,按分支序号升序稳定输出。
// hedgeAccs 为「对冲分支序号 → 已占账号」,序号即点火顺序(buildHedge 按 1..N 依次构造),
// 与裁决日志里的 winnerHedgeIdx 一一对应;nil 账号防御性输出 <nil>,不 panic。
// 调用方仅在裁决时刻渲染一次,回答日志读者「这场比赛谁参赛、谁胜出」。
func formatNvidiaHedgeRace(primaryEmail string, hedgeAccs map[int]*account.Account) string {
	var sb strings.Builder
	sb.WriteString("主[")
	sb.WriteString(primaryEmail)
	sb.WriteString("]")
	if len(hedgeAccs) == 0 {
		return sb.String()
	}
	idxs := make([]int, 0, len(hedgeAccs))
	for idx := range hedgeAccs {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		email := "<nil>"
		if acc := hedgeAccs[idx]; acc != nil {
			email = acc.Email
		}
		fmt.Fprintf(&sb, "+对冲%d[%s]", idx, email)
	}
	return sb.String()
}
