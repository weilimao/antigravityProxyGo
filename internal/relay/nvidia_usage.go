package relay

// nvidia_usage.go 收纳 NVIDIA 用量记录与选号链路。
// 从 nvidia.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。
//
// 本文件覆盖:
//   - (h *APICompatHandler) recordNvidiaUsage  五落点用量记录(relay/usage/nvidiaTrends/全局/请求日志)
//   - isAnthropicPassthroughHeader             Anthropic 专属头判定(不透传)
//   - errStr                                   error 安全转字符串
//   - (h *APICompatHandler) pickNvidiaAccount  选号统一入口(sticky / round-robin)

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// recordNvidiaUsage 记录 NVIDIA 用量。
// 一处落点：relayStatsMgr(RelaySample/relay_stats.json，按中继 UserID 分桶，用于中继用户维度统计与按 Key 限额回填)；
// 另一处落点：usageTracker(UsageSample/usage.json，按号池成员账号 AccountMeta 分桶，用于前端“账号使用统计”页，
//
//	使每个 NVIDIA 号池账号的请求次数/Token/成本/模型可见，与 Gemini/claude 直连链路口径一致)。
//
// ModelName 在 relayStatsMgr 侧带 "nvidia/" 前缀，使 DB 的 family LIKE 查询("nvidia/") 能命中 NVIDIA 族，
// 不污染 gemini/claude 统计；usageTracker 侧去前缀喂入，前端模型列显示为 upstreamModel(如 z-ai/glm-5.2)，
// pricing 的 fuzzy 匹配仍能按子串(kimi/llama/nemotron)命价。
func (h *APICompatHandler) recordNvidiaUsage(userSession *RelaySession, model string, input, output int, poolAccount *account.Account, logCtx nvidiaLogCtx) {
	if input == 0 && output == 0 {
		return
	}

	// 1) 中继用户维度统计(relay_stats.json) + 按 API Key 限额回填
	if h.statsTracker != nil && userSession != nil {
		prefixedModel := model
		if !strings.HasPrefix(model, "nvidia/") {
			prefixedModel = "nvidia/" + model
		}
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:      fmt.Sprintf("nv-%d", time.Now().UnixNano()),
			UserID:     userSession.UserID,
			UserKey:    userSession.UserKey,
			ModelName:  prefixedModel,
			InTokens:   input,
			OutTokens:  output,
			Method:     "POST",
			Host:       "nvidia",
			// Path 用 logCtx.Path(由 writeNvidiaResponse 从入站 r.URL.Path 装配),反映真实入站前缀:
			// /nvidia/* 记 "/nvidia/v1/messages" 等,别名 /vc/* 记 "/vc/v1/messages" 等,
			// 不再写死 "/nvidia"——relay_stats.json 可区分两条别名入口的流量分布。
			// 与落点5「请求日志」Path: logCtx.Path(nvidia_usage.go:126)口径一致。
			Path:       logCtx.Path,
			StatusCode: 200,
		})

		// 单 API Key 的 NVIDIA 用量回填（与 UsedNvidiaTokens 配合形成按 Key 限额）。
		// 与 gemini/claude 链路(app.go proxyHandler 回调 RecordAPIKeyUsage)对齐。
		if h.authMgr != nil && h.authMgr.userMgr != nil && userSession.APIKeyID != "" {
			h.authMgr.userMgr.RecordAPIKeyUsage(userSession.UserID, userSession.APIKeyID, false, int64(input+output))
		}
	}

	// 2) 号池成员账号维度统计(usage.json) —— 复用 Gemini/claude 直连链路同样口径。
	// NVIDIA 上游走 OpenAI Chat 协议，响应 usage 无 cache 概念，CachedTokens 置 0。
	if h.usageTracker == nil || userSession == nil {
		return
	}
	var accMeta *stats.AccountMeta
	if poolAccount != nil {
		accMeta = &stats.AccountMeta{
			ID:        poolAccount.ID,
			Email:     poolAccount.Email,
			Provider:  poolAccount.Provider,
			ProjectID: poolAccount.ProjectID,
			ScopeType: poolAccount.ScopeType,
		}
	}
	displayModel := model
	if strings.HasPrefix(displayModel, "nvidia/") {
		displayModel = strings.TrimPrefix(displayModel, "nvidia/")
	}
	h.usageTracker.RecordUsage(stats.UsageSample{
		ModelName:    displayModel,
		InTokens:     input,
		OutTokens:    output,
		CachedTokens: 0,
		Account:      accMeta,
	})

	// 3) 全局「使用趋势-NVIDIA」专用桶 (stats.Tracker.TrackNvidiaRequest)。
	// 这是让英伟达号池用量首次进入仪表盘使用趋势图的关键落点: 仅累加全局 stats.Tracker 的
	// nvidiaTrends 桶, 不进 trends 综合桶, 也不动全局 stats/Models, 因此「综合趋势」Tab
	// 与顶部指标卡口径完全不变(零回归), 「NVIDIA」Tab 单独反映号池时间曲线。
	// globalStatsTracker 未注入(relay 单测场景)时为 nil, 安全跳过, 不影响既有两路统计。
	// displayModel 已去 "nvidia/" 前缀, 满足 TrackNvidiaRequest 对上游展示名的约定;
	// NVIDIA 上游(OpenAI Chat 协议)无 cache, cachedTokens 固定 0。
	if h.globalStatsTracker != nil {
		h.globalStatsTracker.TrackNvidiaRequest(displayModel, input, output)

		// 4) 全局综合统计 (stats.Tracker.TrackRequestForModel): 把同一笔 NVIDIA 请求首次计入
		// 顶部指标卡 + stats.Models 模型表 + trends 综合趋势桶, 使其与 gemini/claude 直连链路
		// 口径一致。TrackRequestForModel 累加口径与 TrackRequest 同构, 仅写全局桶, 与 nvidiaTrends
		// 物理隔离桶互不污染——故「综合趋势」(全局含NVIDIA)与「使用趋势-NVIDIA」(纯NVIDIA)是
		// 两个不同口径视图, 不构成错误的双重计数。
		// 与落点1(relay 维度 RecordUsage, 模型名带 nvidia/ 前缀, 供 relay:get-user-stats 族查询)
		// 数据源隔离: 落点4 写 stats.json(主仪表盘), 落点1 写 relay_stats.json(中继用户维度页),
		// 二者走不同 IPC/不同 Tab, 无前端汇总相加逻辑, 故去前缀 vs 带前缀不产生叠加误导。
		// NVIDIA 上游无 cache, cachedTokens 固定 0。
		h.globalStatsTracker.TrackRequestForModel(displayModel, input, output, 0)

		// 5) 请求日志 (stats.Tracker.AddRequestLogForFamily): 把 NVIDIA 成功请求写入仪表盘
		// 「请求日志」列表。绕过既有 AddRequestLog 的 isRealModel 过滤(要求 Path 含
		// generatecontent/predict, NVIDIA 走 /v1/chat/completions 不满足), 由 family 显式入库。
		// Model 用去前缀上游展示名, 与「模型统计」展示口径一致; CacheStatus="NONE"
		// (NVIDIA 上游 OpenAI Chat 协议无 cache, 前端紫色 NONE badge 自动渲染); DurationMs 由
		// handleNvidia 入口 startTs 算得端到端耗时, 极快返回时下限保底 1ms 避免 0ms 误读。
		// ID 经原子序列 nvidiaReqLogSeq 去碰撞, 与 relay 维度落点1 的 ReqID 命名空间分离(便于对照排查)。
		durationMs := time.Since(logCtx.StartTs).Milliseconds()
		if durationMs <= 0 {
			durationMs = 1
		}
		reqLog := &stats.RequestLog{
			ID:           fmt.Sprintf("nvlog-%d-%d", atomic.AddUint64(&nvidiaReqLogSeq, 1), time.Now().UnixNano()),
			Timestamp:    time.Now().Format("01/02 15:04:05"),
			Method:       logCtx.Method,
			Host:         logCtx.Host,
			Path:         logCtx.Path,
			Model:        displayModel,
			InTokens:     input,
			OutTokens:    output,
			CachedTokens: 0,
			CacheStatus:  "NONE",
			StatusCode:   logCtx.StatusCode,
			Account:      logCtx.Account,
			SessionID:    logCtx.SessionID,
			DurationMs:   durationMs,
			Family:       "nvidia",
		}
		h.globalStatsTracker.AddRequestLogForFamily(reqLog)
	}
}

// isAnthropicPassthroughHeader 判断是否为 anthropic 专属头(不透传给客户端)。
func isAnthropicPassthroughHeader(k string) bool {
	switch strings.ToLower(k) {
	case "anthropic-version", "anthropic-beta", "x-api-key", "x-goog-api-key":
		return true
	}
	return false
}

func errStr(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// pickNvidiaAccount 是 NVIDIA 选号统一入口, 兼顾 /nvidia/v1/models 与 /nvidia/{messages,chat/completions,responses} 两处调用点,
// 兼容 sticky 粘性与 round-robin 两种 LB 模式, 统一接入"每账号最近 1 分钟请求计数盘"。
//
// 选号语义:
//   - sticky 模式: 走 sessionRouter.GetOrAssignAccount 保持原哈希粘性语义不变, 仅在选定后 Tick 计数,
//     使其负载被如实记录(跨模式信息不割裂)。
//   - round-robin 模式: 按 nvidiaStats 的最近 1 分钟计数"最少优先", 计数相同时用全局游标
//     nvidiaCursor 取模打破平局。首轮所有账号计数 == 0 时候选集合含全部账号, 退化为原取模轮询,
//     既有行为/测试断言自动兼容。
//
// 选定后无论哪种模式均调用 nvidiaStats.Tick 记录本次占用, 为后续选号提供"窗口内已承担请求次数"信号。
//
// sessionKey / sessionRouter 仅 sticky 路径使用, round-robin 路径不依赖, 允许为空。
// 返回 nil 仅当入参 accounts 为空。
func (h *APICompatHandler) pickNvidiaAccount(lbMode, sessionKey string, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}

	var assigned *account.Account
	if lbMode == "sticky" && h.sessionRouter != nil {
		assigned = h.sessionRouter.GetOrAssignAccount(sessionKey, accounts, h.logFn)
	} else {
		// round-robin 最少计数优先 + 游标打破平局
		ids := make([]string, len(accounts))
		for i, a := range accounts {
			ids[i] = a.ID
		}
		candidates, _ := h.nvidiaStats.pickLeastCountIndex(ids)
		cursor := atomic.AddUint64(&h.nvidiaCursor, 1) - 1
		idx := candidates[int(cursor%uint64(len(candidates)))]
		assigned = accounts[idx]
	}

	if assigned != nil && h.nvidiaStats != nil {
		// 如实记录本次占用, 为突发高并发洪流提供"窗口内已承担次数"信号。
		// 注:即便选号读到的计数已偏高, 这里仍追加记录, 让后续选号感知真实负载。
		h.nvidiaStats.Tick(assigned.ID)
	}
	return assigned
}
