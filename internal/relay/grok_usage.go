package relay

// grok_usage.go: Grok(x.ai) 号池的请求日志/统计落库链路与选号器。
//
// 与 nvidia_usage.go(recordNvidiaUsage / pickNvidiaAccount)与 passthrough_usage.go
// (recordOtherUsage)三处对偶, 形态逐行等价, 仅替换族标识与池 key:
//   - relay 维度模型名带 "grok/" 前缀, 与 "nvidia/" 族在 relay_stats.json 物理隔离;
//   - 号池成员账号维度(usage.json)去前缀展示上游真实模型名(grok-4.3 等);
//   - 全局综合统计(TrackRequestForModel + TrackRequestForPool("grok"))把 Grok 用量计入
//     顶部指标卡 / 模型统计表 / 综合趋势桶 / 缓存命中率「grok」子桶;
//   - 请求日志 Family="grok" 绕过 AddRequestLog 的 isRealModel 过滤显式入库。
//   - 不走 nvidiaTrends 专用桶(那是 NVIDIA 独立的物理隔离曲线);grok 与 gemini/claude/other
//     同走综合 trends 桶 + 独立 Pools["grok"] 子聚合, 前端「使用趋势」综合视图可见。
//
// 选号器 pickGrokAccount 与 pickOtherAccount 同构(sticky 粘性 + grokCursor 取模轮询),
// 不接 nvidiaStats 那套「1 分钟请求计数盘」(Grok 流量小, 最少计数语义无显著收益, 后续按需可扩展)。

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/stats"
)

// grokChannel 是 Grok 号池的通道标识, 与 account.GetModelCategoryByProvider("grok"→"grok")
// 冷却类别同源, 供 GetAvailableAccountsForChannel("grok", ...) 选号池过滤。
const grokChannel = "grok"

// grokReqLogSeq 是 Grok 请求日志(落点5)的全局原子递增序列, 用于生成稳定且无碰撞的 RequestLog.ID。
// 与 nvidiaReqLogSeq / otherReqLogSeq 同构: 单独纳秒时间戳高并发易碰撞, 叠加单调递增序列后即使
// 同纳秒也唯一; ID 用 "groklog-" 前缀与 NVIDIA/Other 请求日志命名空间分离, 便于把同一笔请求在
// 跨族日志里对照排查。
var grokReqLogSeq uint64

// grokLogCtx 携带一次 Grok 请求落地为「请求日志」+「全局综合统计」所需的最小上下文, 与 nvidiaLogCtx
// / passthroughLogCtx 逐字段同构。由 writeGrokResponse 在分发回写协议前统一装配, 供四个下行分支
// 的 recordGrokUsage 调用点共享。
//
// Host 优先取上游账号 BaseURL 的裸 host; poolAccount 为空时优先用入站 r.Host, 再回退占位 "grok"。
// Path/Method 取自入站 r; Account 优先号池 Email, 缺则 userSession.UserID; SessionID 经
// ocrSessionDisplay 取 userSession.SessionKey(auth:acc:<16hex> 口径, 与 antigravity/nvidia 链路
// 同款)。StartTs/FirstByteRec 由 handleGrok 入口起算并全程共享, DurationMs 取流式耗时、FirstByteMs 取 TTFT。
type grokLogCtx struct {
	Method       string
	Host         string
	Path         string
	SessionID    string
	Account      string
	StatusCode   int
	StartTs      time.Time
	FirstByteRec *stats.FirstByteRecorder
	// ReqBody 是入站请求体经 parseInboundBodyForLog 解析后的结构化值(空 → nil, 合法 JSON →
	// interface{}, 非 JSON → 原始字符串), 供 recordGrokUsage 落库为 stats.RequestLog.RequestBody,
	// 使前端「请求参数详情」弹窗能展示入站请求体而非「无请求参数」兜底。由 writeGrokResponse
	// 装配 logCtx 时从入站 bodyBytes 注入; 超长字段后续由 stats.TruncateRequestBody 统一截断防 OOM。
	ReqBody interface{}
	// ReqHeaders 是入站请求头经 collectInboundHeadersForLog 采集(含敏感头脱敏)后的
	// {键: 值} 映射, 供 recordGrokUsage 落库为 stats.RequestLog.RequestHeaders, 使前端详情弹窗
	// 能展示入站请求头而非「无请求头数据」兜底。
	ReqHeaders interface{}
}

// grokHostFromBaseURL 从上游账号 BaseURL(如 https://api.x.ai/v1)提取裸 host(如 api.x.ai),
// 与 nvidiaHostFromBaseURL / passthroughHostFromBaseURL 同构, 仅回退占位用 "grok"。
// 复用 nvidiaHostFromBaseURL 的解析逻辑, 保持三池口径一致。
func grokHostFromBaseURL(baseURL string) string {
	h := nvidiaHostFromBaseURL(baseURL)
	if h == "" || h == "nvidia" {
		return "grok"
	}
	return h
}

// recordGrokUsage 记录 Grok 一次成功请求的用量到五处落点, 与 recordNvidiaUsage / recordOtherUsage
// 对偶: input==0 && output==0 时整函数早退(避免空桶/噪声)。
// userSession 为 nil 时跳过落点1/2(单测/未注入场景), 但不影响落点3/4(globalStatsTracker 注入后
// 仍可记录, 与 recordNvidiaUsage 降级语义一致)。
// cached 为上游回报的缓存命中 token(xAI 官方端点可能支持 prompt caching), 决定 CacheStatus 与
// 缓存命中率分子: >0 → "HIT", 否则 "NONE"。
//
// 各落点复用既有基础设施, 仅在「族标识/池 key/模型名前缀」三处替换为 grok, 既有调用方零回归:
//   - 落点1  中继用户维度统计(relay_stats.json) + 按 API Key 限额回填, 模型名带 "grok/" 前缀;
//   - 落点2  号池成员账号维度统计(usage.json, 前端「账号使用统计」页), 去前缀展示上游真实模型名;
//   - 落点3  全局综合统计(TrackRequestForModel → 顶部指标卡 + stats.Models 模型表 + trends 综合趋势);
//   - 落点3b 号池命中率筛选(TrackRequestForPool("grok") → Pools["grok"] 子聚合, 不动全局标量);
//   - 落点4  请求日志(AddRequestLogForFamily, family="grok"), 绕过 isRealModel 过滤显式入库。
func (h *APICompatHandler) recordGrokUsage(userSession *RelaySession, model string, input, output, cached int, poolAccount *account.Account, logCtx grokLogCtx) {
	if input == 0 && output == 0 {
		return
	}

	// 1) 中继用户维度统计(relay_stats.json) + 按 API Key 限额回填。
	// ModelName 带 "grok/" 前缀, 使 DB 的 family LIKE 查询("grok/") 能命中 Grok 族, 不污染
	// gemini/claude/nvidia 统计; 与 recordNvidiaUsage 的 "nvidia/" 前缀口径同构。
	if h.statsTracker != nil && userSession != nil {
		prefixedModel := model
		if !strings.HasPrefix(model, "grok/") {
			prefixedModel = "grok/" + model
		}
		h.statsTracker.RecordUsage(RelaySample{
			ReqID:     fmt.Sprintf("grok-%d", time.Now().UnixNano()),
			UserID:    userSession.UserID,
			UserKey:   userSession.UserKey,
			ModelName: prefixedModel,
			InTokens:  input,
			OutTokens: output,
			Method:    "POST",
			Host:      "grok",
			// Path 用 logCtx.Path(由 writeGrokResponse 从入站 r.URL.Path 装配), 反映真实入站前缀:
			// /grok/* 记 "/grok/v1/messages" 等, 别名 /xai/* 记 "/xai/v1/messages" 等,
			// /route/* 路由命中记 "/route/..."; 与落点4「请求日志」Path 口径一致。
			Path:       logCtx.Path,
			StatusCode: 200,
		})

		// 单 API Key 的 Grok 用量回填(与 NVIDIA / gemini/claude 链路对齐)。
		// 方案 A: 走 family-aware 后继 RecordAPIKeyUsageForFamily(FamilyGrok), 把用量计入
		// 独立 UsedGrokTokens 桶, 与 app_lifecycle.go 的 LimitGrokTokens 限额校验正交自洽;
		// 不沿用 RecordAPIKeyUsage 的 isClaude 二态(会把 Grok 流量误落 Gemini 桶), 与 NVIDIA
		// 既有调用点零串扰(其仍走 RecordAPIKeyUsage(false)。
		if h.authMgr != nil && h.authMgr.userMgr != nil && userSession.APIKeyID != "" {
			h.authMgr.userMgr.RecordAPIKeyUsageForFamily(userSession.UserID, userSession.APIKeyID, FamilyGrok, int64(input+output))
		}
	}

	// 2) 号池成员账号维度统计(usage.json) —— 复用既有 usageTracker(账号使用统计页), 去前缀展示。
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
	if strings.HasPrefix(displayModel, "grok/") {
		displayModel = strings.TrimPrefix(displayModel, "grok/")
	}
	h.usageTracker.RecordUsage(stats.UsageSample{
		ModelName:    displayModel,
		InTokens:     input,
		OutTokens:    output,
		CachedTokens: cached,
		Account:      accMeta,
	})

	// 3/3b/4) 全局综合统计 + 号池命中率子聚合 + 请求日志(globalStatsTracker)。
	// 落点3 使 Grok 请求首次计入顶部指标卡 + 模型统计表 + 综合趋势桶;
	// 落点3b 把同一笔写入 Pools["grok"] 子聚合(缓存命中率分子分母独立累加, 不动全局标量);
	// 落点4 把 Grok 请求写入仪表盘「请求日志」列表(绕过 AddRequestLog 的 isRealModel 过滤,
	// 由 family="grok" 显式入库, 与 NVIDIA/Other 的 AddRequestLogForFamily 同策略)。
	// globalStatsTracker 未注入(relay 单测场景)时为 nil, 安全跳过这三个落点, 不影响落点1/2。
	if h.globalStatsTracker != nil {
		h.globalStatsTracker.TrackRequestForModel(displayModel, input, output, cached)

		// 号池命中率筛选: Grok 池独立累加分子分母到 Pools["grok"] (与 TrackRequestForModel 并行,
		// 只写 Pools 子聚合, 不动全局标量)。poolAccount nil 时无"直连归 grok"语义, 跳过避免误归。
		if poolAccount != nil {
			h.globalStatsTracker.TrackRequestForPool(displayModel, input, output, cached, "grok")
		}

		// DurationMs 采用「第一帧→流结束」的流式耗时口径(StreamDurationMs, 不含 TTFT,
		// 与前端「响应时间」列语义分离); TTFT(FirstByteMs) 仍为请求→首帧的端到端截断;
		// FirstByteRec 为 nil(防御)时 TTFT 兜底为端到端耗时。
		end := time.Now()
		endToEndMs := end.Sub(logCtx.StartTs).Milliseconds()
		durationMs := int64(0)
		if logCtx.FirstByteRec != nil {
			durationMs = logCtx.FirstByteRec.StreamDurationMs(end)
		}
		if durationMs <= 0 {
			durationMs = 1
		}
		var firstByteMs int64
		if logCtx.FirstByteRec != nil {
			firstByteMs = logCtx.FirstByteRec.FirstByteMs(endToEndMs)
		} else {
			firstByteMs = endToEndMs
		}
		cacheStatus := "NONE"
		if cached > 0 {
			cacheStatus = "HIT"
		}
		// 原子序列保证高并发下 RequestLog.ID 无碰撞(同纳秒也唯一, 见 nvidiaReqLogSeq 注释)。
		seq := atomic.AddUint64(&grokReqLogSeq, 1)
		reqLog := &stats.RequestLog{
			ID:             fmt.Sprintf("groklog-%d-%d", seq, rand.Intn(1000)),
			Timestamp:      time.Now().Format("01/02 15:04:05"),
			Method:         logCtx.Method,
			Host:           logCtx.Host,
			Path:           logCtx.Path,
			Model:          displayModel,
			InTokens:       input,
			OutTokens:      output,
			CachedTokens:   cached,
			CacheStatus:    cacheStatus,
			StatusCode:     logCtx.StatusCode,
			Account:        logCtx.Account,
			RequestBody:    logCtx.ReqBody,
			RequestHeaders: logCtx.ReqHeaders,
			SessionID:      logCtx.SessionID,
			DurationMs:     durationMs,
			FirstByteMs:    firstByteMs,
			Family:         "grok",
		}
		h.globalStatsTracker.AddRequestLogForFamily(reqLog)
	}
}

// pickGrokAccount 是 Grok 选号统一入口, 兼顾 /grok/v1/models 与 /grok/{chat/completions,messages,responses}
// (以及 /xai 别名、/route 命中 grok)两处调用点, 兼容 sticky 粘性与 round-robin 两种 LB 模式。
//
// 选号语义(与 pickOtherAccount 同构, 不接 nvidiaStats 1 分钟计数盘):
//   - sticky 模式: 走 sessionRouter.GetOrAssignAccount 保持原哈希粘性语义;
//   - round-robin 模式: 按全局游标 grokCursor 取模轮询, 单调递增打破共振;
//   - 首轮/单账号场景退化为恒取唯一号, 既有行为/测试断言自动兼容。
//
// sessionKey / sessionRouter 仅 sticky 路径使用, round-robin 路径不依赖, 允许为空。
// 返回 nil 仅当入参 accounts 为空。
func (h *APICompatHandler) pickGrokAccount(lbMode, sessionKey string, accounts []*account.Account) *account.Account {
	if len(accounts) == 0 {
		return nil
	}

	if lbMode == "sticky" && h.sessionRouter != nil {
		assigned := h.sessionRouter.GetOrAssignAccount(sessionKey, accounts, h.logFn)
		if assigned != nil {
			return assigned
		}
		// GetOrAssignAccount 极少返回 nil(边界防御), 兜底走 round-robin。
	}
	// round-robin:全局游标取模轮询(与 pickOtherAccount 同构, 无 nvidiaStats 计数依赖)。
	cursor := atomic.AddUint64(&h.grokCursor, 1) - 1
	idx := int(cursor % uint64(len(accounts)))
	return accounts[idx]
}
