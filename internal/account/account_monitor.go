package account

import (
	"errors"
	"fmt"
	"time"
)

// account_monitor.go 收纳定时监控与 Token 刷新。
// 拆分自原 account.go:此处只保留「冷却监控/配额刷新/Token 刷新/错误计数/配额回写」逻辑;
// Manager 容器/CRUD 见 account_manager.go,选号/冷却逻辑见 account_selector.go。
//
// conc:
//   - 监控 goroutine 通过 cooldownStop/tokenRefreshStop chan 优雅退出,启停均持锁改 ticker;
//   - RecordAccountError 在 5 次阈值达成的临界区清零计数,避免并发多次触发 FetchQuota;
//   - RefreshAccountTokenSync 用 getAccountRefreshLock 做单账号互斥 + 双次 30s 复用检查;
//   - CheckAndRefreshTokens 用 tokenRefreshMaxConcurrent 信号量跨账号节流(见下常量)。

// tokenRefreshMaxConcurrent 是 CheckAndRefreshTokens 同时发起刷新的最大账号并发数。
// 历史「每账号一个 go func 无上限」在批量导入后会把几十个号同时打到 auth.x.ai,
// 触发上游风控 + refresh_token 轮换竞态导致 invalid_grant 误停用(详见导入场景案例)。
// 3 = 经验值:批量刷新推进够快,又远低于 xAI 风控阈值。
const tokenRefreshMaxConcurrent = 3

// tokenRefreshSkewSec 是「access_token 仍有效」提前量(秒)。JWT exp 距当前 > 此值即跳过当前
// tick 的刷新,避免对有效 token 无谓打刷新端点。reserved 10 分钟≈一个补刷窗口,临近再刷即可。
const tokenRefreshSkewSec = 10 * 60

// ============ 冷却监控(2 分钟 tick) ============

func (m *Manager) StartCooldownMonitor() {
	m.Lock()
	if m.cooldownTicker != nil {
		m.Unlock()
		return
	}
	m.cooldownTicker = time.NewTicker(2 * time.Minute)
	m.cooldownStop = make(chan struct{})
	// 捕获为局部变量供 goroutine 闭包引用,而非每轮 select 重新读 m.cooldownTicker / m.cooldownStop。
	// 背景:StopCooldownMonitor 会先 m.cooldownTicker.Stop() 再 m.cooldownTicker=nil 再 close(stop),
	// 若 goroutine 每轮 select 重读 m.cooldownTicker.C,在「Stop 已置 nil 但 close(stop) 尚未生效」
	// 的窗口内重入 select 会读 nil.C 触发 panic。局部捕获后 goroutine 永远引用非 nil 的 ticker 句柄,
	// close(stop) 立即唤醒 return 分支,与 Stop 置 nil m.cooldownTicker 正交,消除该窗口竞态。
	ticker := m.cooldownTicker
	stop := m.cooldownStop
	m.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				m.CheckCooldownAccounts()
			case <-stop:
				return
			}
		}
	}()
}

// CheckCooldownAccounts 扫描处于冷静期且已到期的账号，尝试拉取最新配额或解除冷静状态。
func (m *Manager) CheckCooldownAccounts() {
	m.RLock()
	var cooldownAccounts []*Account
	now := time.Now().UnixNano() / int64(time.Millisecond)
	for _, a := range m.accounts {
		// 只有处于启用（Enabled）状态的冷静期到期账号，才被允许自动拉取配额
		if a.Enabled && a.CooldownUntil > 0 && now >= a.CooldownUntil {
			cooldownAccounts = append(cooldownAccounts, a)
		}
	}
	m.RUnlock()

	if len(cooldownAccounts) == 0 {
		return
	}

	if m.FetchQuota == nil {
		// 如果未注册配额拉取回调，直接解除冷静状态
		m.Lock()
		touchedProviders := map[string]struct{}{}
		for _, acc := range cooldownAccounts {
			acc.CooldownUntil = 0
			acc.Cooldowns = make(map[string]int64)
			if acc.Provider != "" {
				touchedProviders[acc.Provider] = struct{}{}
			}
		}
		m.Unlock()
		// 定向落盘:只重写被解除冷静期账号涉及的 provider 分区,不触碰其它号池大文件。
		// cooldownAccounts 跨多 provider 时各分区分别重写,未触及号池不重写。
		kinds := make([]string, 0, len(touchedProviders))
		for p := range touchedProviders {
			kinds = append(kinds, p)
		}
		if len(kinds) > 0 {
			_ = m.SaveAccountsFor(false, kinds...)
		} else {
			_ = m.SaveAccounts(false)
		}
		return
	}

	for _, acc := range cooldownAccounts {
		// 异步刷新验证
		go func(a *Account) {
			fmt.Printf("[CooldownMonitor] Verifying quota for cooled account: %s\n", a.Email)
			res, err := m.FetchQuota(a)
			if err != nil {
				// 刷新失败，冷静期往后延长 5 分钟
				m.Lock()
				targetAcc := m.getAccountByIDLocked(a.ID)
				cooldownProvider := ""
				if targetAcc != nil {
					cooldownProvider = targetAcc.Provider
					nextCooldown := time.Now().UnixNano()/int64(time.Millisecond) + 5*60*1000
					targetAcc.CooldownUntil = nextCooldown
					if targetAcc.Cooldowns != nil {
						for k := range targetAcc.Cooldowns {
							targetAcc.Cooldowns[k] = nextCooldown
						}
					}
				}
				m.Unlock()
				// 定向落盘:只重写该账号所属 provider 分区,不触碰其它号池大文件。
				// 空 provider 兜底走全量(targetAcc 非空时 provider 必非空,此处为防御)。
				if cooldownProvider != "" {
					_ = m.SaveAccountsFor(true, cooldownProvider)
				} else {
					_ = m.SaveAccounts(true)
				}
				return
			}

			if res != nil {
				m.UpdateAccountQuota(a.ID, res)
			}
		}(acc)
	}
}

func (m *Manager) StopCooldownMonitor() {
	m.Lock()
	defer m.Unlock()
	if m.cooldownTicker != nil {
		m.cooldownTicker.Stop()
		m.cooldownTicker = nil
		close(m.cooldownStop)
	}
}

// ============ Token 刷新监控(5 分钟 tick) ============

func (m *Manager) StartTokenRefreshMonitor() {
	m.Lock()
	if m.tokenRefreshTicker != nil {
		m.Unlock()
		return
	}
	m.tokenRefreshTicker = time.NewTicker(5 * time.Minute)
	m.tokenRefreshStop = make(chan struct{})
	// 局部捕获(同 StartCooldownMonitor):消除 Stop 置 nil 与 select 重读 m.tokenRefreshTicker.C 的窗口竞态。
	ticker := m.tokenRefreshTicker
	stop := m.tokenRefreshStop
	m.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				m.CheckAndRefreshTokens()
			case <-stop:
				return
			}
		}
	}()
}

func (m *Manager) CheckAndRefreshTokens() {
	m.RLock()
	var refreshAccounts []*Account
	nowSec := time.Now().Unix()
	for _, a := range m.accounts {
		// grok 与 workbuddy 号池脱离本全局 5min tick:
		// grok 走专用 1h 节奏的 CheckAndPurgeGrokAuth;
		// workbuddy 为 Keycloak JWT / 独立凭证体系，不可走 Google OAuth 刷新流，避免启动期向 Google 发送无效请求。
		if a.Provider == "grok" || a.Provider == "workbuddy" {
			continue
		}
		// 仅对已启用，有刷新Token和AccessToken的非2fa账号做定时刷新
		// 判断时间是否超过50分钟 (50 * 60 = 3000 秒)
		// a.TokenRefreshedAt 如果是 0，说明还没存过，应当刷新一次进行初始化记录
		if a.Enabled && a.RefreshToken != "" && a.AccessToken != "" && a.Provider != "2fa" {
			refreshedAt := a.GetTokenRefreshedAt()
			if refreshedAt == 0 || (nowSec-refreshedAt) > 50*60 {
				refreshAccounts = append(refreshAccounts, a)
			}
		}
	}
	m.RUnlock()

	if len(refreshAccounts) == 0 {
		return
	}

	if m.RefreshToken == nil {
		return
	}

	// 并发限流:历史上对每个待刷账号直接 go func() 无上限并发,当一次导入几十个 OAuth 账号
	// (如 grok OAuth 号池)后,下一个 tick 会瞬间把全部账号并发打到 auth.x.ai/oauth2/token,
	// 触发上游风控/限流,且并发用同一 refresh_token 会因 token 轮换返回 invalid_grant 被误停用。
	// 改为 tokenRefreshMaxConcurrent 信号量节流:同一时刻最多这么多账号在刷,主 goroutine 在
	// `refreshSlot <- struct{}{}` 上排队等空位,天然的串行化闸门(无需额外 wg.Wait 阻塞,保持 fire-and-forget)。
	// 值取 3:足够推进批量刷新,又不至于把 xAI 端打风控,且与 RefreshAccountTokenSync 单账号互斥
	// 语义不冲突——RefreshAccountTokenSync 串行化「同一账号」,这里节流「不同账号并行数」。
	refreshSlot := make(chan struct{}, tokenRefreshMaxConcurrent)
	for _, acc := range refreshAccounts {
		// access_token 仍是有效 JWT 且离过期还有 > tokenRefreshSkewSec 时跳过本次刷新。
		// 背景:导入 accounts_export 时 access_token 的 exp(如 1786472417)可能远未到期,
		// TokenRefreshedAt=0 会触发"从未刷新"分支,对本就有效的 token 白白发一次刷新请求,
		// 既无意义又增加被上游风控的概率。仅当无法判定 exp(=0,如非 JWT 的 API Key 账号)
		// 或临近过期(<=tokenRefreshSkewSec)时才真的刷新。
		// 注意:provider=grok 的 access_token 是 JWT(可解析 exp);provider=antigravity 亦是;
		// nvidia/other 的 access_token 是 API Key(非 JWT,exp=0),走原"无 exp 信息→仍刷新"分支,
		// 行为与历史一致(它们本就不该命中本分支——非 2fa + 有 RefreshToken 的主要是 OAuth 账号)。
		if exp := acc.AccessTokenExp(); exp > 0 && (exp-nowSec) > tokenRefreshSkewSec {
			continue
		}
		refreshSlot <- struct{}{}
		go func(a *Account) {
			defer func() { <-refreshSlot }()
			fmt.Printf("[TokenRefreshMonitor] Automatically refreshing token for account: %s\n", a.Email)
			newToken, err := m.RefreshToken(a)
			if err != nil {
				fmt.Printf("[TokenRefreshMonitor] Failed to auto-refresh token for %s: %v\n", a.Email, err)
				return
			}
			m.UpdateAccessToken(a.ID, newToken)
			fmt.Printf("[TokenRefreshMonitor] Successfully refreshed token for %s\n", a.Email)
		}(acc)
	}
}

func (m *Manager) StopTokenRefreshMonitor() {
	m.Lock()
	defer m.Unlock()
	if m.tokenRefreshTicker != nil {
		m.tokenRefreshTicker.Stop()
		m.tokenRefreshTicker = nil
		close(m.tokenRefreshStop)
	}
}

// ============ Grok 号池专用「授权过期检查→刷新→失效移除」监控(1 小时 tick) ============

// grokAuthRefreshSkewSec 是 grok 专用「access_token 仍有效」提前量(秒)。JWT exp 距当前
// <= 此值才触发刷新。取 60 分钟:与 1 小时 tick 自洽——最坏「检查时剩 61min 被跳过 →
// 1h 后剩 1min 立即刷新」,access_token(JWT 寿命 6h)不会在两次 tick 间裸奔过期。
// 与全局 tokenRefreshSkewSec(10min,服务 antigravity/project/google)正交,grok 专走本值。
const grokAuthRefreshSkewSec = 60 * 60

// grokAuthCheckInterval 是 Grok 授权过期检查的 tick 间隔(1 小时)。
// 用户语义:刷新频率由全局 5min 降到 1h(仅 grok),通过 grokAuthRefreshSkewSec 提前量兜底不裸奔。
const grokAuthCheckInterval = 60 * time.Minute

// GrokAuthCheckResult 是单个 grok 账号一次授权检查的结果,供 IPC 汇总回前端展示。
// Outcome 取值:
//   - "skipped":access_token 仍有效(exp 距当前 > grokAuthRefreshSkewSec),未刷新;
//   - "refreshed":临近/已过期,刷新成功,新 token 已写入;
//   - "disabled":刷新命中永久失败(invalid_grant 等),已自动停用账号(保留在池内);
//   - "failed":刷新命中瞬时失败(网络/5xx/超时),未停用,留给下个 tick 重试。
type GrokAuthCheckResult struct {
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
}

// StartGrokAuthMonitor 启动 Grok 号池专用 1h tick:每 tick 调 CheckAndPurgeGrokAuth,
// 对所有 grok OAuth 账号做「过期检查→临近过期刷新→永久失败自动停用」。
// 与 StartTokenRefreshMonitor 同构:启停均持锁改 ticker,goroutine 经 stop chan 优雅退出。
// 幂等:已在运行时再次调用直接返回(不重复起 goroutine)。
func (m *Manager) StartGrokAuthMonitor() {
	m.Lock()
	if m.grokAuthTicker != nil {
		m.Unlock()
		return
	}
	m.grokAuthTicker = time.NewTicker(grokAuthCheckInterval)
	m.grokAuthStop = make(chan struct{})
	// 局部捕获(同 StartCooldownMonitor/StartTokenRefreshMonitor):消除 Stop 置 nil 与
	// select 重读 m.grokAuthTicker.C 的窗口竞态。
	ticker := m.grokAuthTicker
	stop := m.grokAuthStop
	m.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				m.CheckAndPurgeGrokAuth()
			case <-stop:
				return
			}
		}
	}()
}

// StopGrokAuthMonitor 停止 Grok 专用授权检查监控,释放 ticker 句柄与后台 goroutine。
// 幂等:未启动时不做任何事(与 StopTokenRefreshMonitor 同口径)。
func (m *Manager) StopGrokAuthMonitor() {
	m.Lock()
	defer m.Unlock()
	if m.grokAuthTicker != nil {
		m.grokAuthTicker.Stop()
		m.grokAuthTicker = nil
		close(m.grokAuthStop)
	}
}

// CheckAndPurgeGrokAuth 对所有 Grok OAuth 账号做一次授权过期检查与刷新。
// 判定主走 access_token(JWT)的 exp claim:
//   - exp > 0 且 (exp - now) > grokAuthRefreshSkewSec:授权仍有效 → skipped;
//   - exp == 0(非 JWT 或解析失败,OAuth grok 一般是 JWT,此分支兜底)或 (exp - now) <= skew:临近/已过期
//     → 调 RefreshAccountTokenSync(单账号互斥 + 30s 复用检查,防抖):
//     成功 → refreshed;失败 → 标 failed(瞬时失败仍启用)或 disabled(永久失败已在 RefreshToken
//     内 UpdateAccountEnabled(false),见 internal/quota/oauth.go refreshXaiToken)。
// 返回每账号结果列表(已停用的号亦在返回中标 disabled,供调用方汇总日志/广播前端)。
//
// 串行逐号刷新(不并发):grok OAuth 号池规模小,串行避免把多号同时打到 auth.x.ai 触发风控。
func (m *Manager) CheckAndPurgeGrokAuth() []GrokAuthCheckResult {
	nowSec := time.Now().Unix()
	// 先取一份 grok OAuth 账号快照(RLock 临界区),逐号串行刷新用 RefreshAccountTokenSync
	// 内部自带单账号互斥,不持 Manager 锁打网络。
	m.RLock()
	var grokAccounts []*Account
	for _, a := range m.accounts {
		// 仅 grok 且带 RefreshToken 的 OAuth 账号才参与;API Key 型 grok 账号(RefreshToken=="")
		// 无 OAuth 刷新概念,跳过。
		if a.Provider == "grok" && a.RefreshToken != "" {
			grokAccounts = append(grokAccounts, a)
		}
	}
	m.RUnlock()

	if len(grokAccounts) == 0 {
		return nil
	}

	var results []GrokAuthCheckResult
	for _, a := range grokAccounts {
		curr := m.GetAccountByID(a.ID)
		if curr == nil {
			continue
		}

		// 仍有效则跳过本次刷新,避免对有效 token 无谓打 xAI 端(防风控抖动)。
		if exp := a.AccessTokenExp(); exp > 0 && (exp-nowSec) > grokAuthRefreshSkewSec {
			results = append(results, GrokAuthCheckResult{
				AccountID: a.ID, Email: a.Email, Outcome: "skipped",
				Detail: "授权仍有效,未刷新",
			})
			continue
		}

		// 临近或已过期(exp==0 兜底也刷新):走单账号互斥刷新(SingleFlight + 30s 复用)。
		_, refErr := m.RefreshAccountTokenSync(a.ID)
		if refErr == nil {
			results = append(results, GrokAuthCheckResult{
				AccountID: a.ID, Email: a.Email, Outcome: "refreshed",
				Detail: "access_token 临近/已过期,刷新成功",
			})
			continue
		}

		// 刷新失败:判定是否已被永久失败链路停用。
		// refreshXaiToken 命中 isPermanent 时已做 UpdateAccountEnabled(false)。
		currAfter := m.GetAccountByID(a.ID)
		if currAfter != nil && !currAfter.Enabled {
			fmt.Printf("[GrokAuthMonitor] Account %s (id=%s) disabled due to permanent refresh failure: %v\n",
				a.Email, a.ID, refErr)
			results = append(results, GrokAuthCheckResult{
				AccountID: a.ID, Email: a.Email, Outcome: "disabled",
				Detail: refErr.Error(),
			})
		} else {
			// 仍在号池且仍启用 → 瞬时失败(网络/5xx/超时),未停用,留待下个 tick 重试。
			fmt.Printf("[GrokAuthMonitor] Account %s (id=%s) transient refresh failure, will retry next tick: %v\n",
				a.Email, a.ID, refErr)
			results = append(results, GrokAuthCheckResult{
				AccountID: a.ID, Email: a.Email, Outcome: "failed",
				Detail: refErr.Error(),
			})
		}
	}
	return results
}

// ============ 错误计数 + 配额回写 ============

func (m *Manager) RecordAccountError(id string, statusCode int, modelName string, logFn func(string)) {
	if statusCode != 503 && statusCode != 429 {
		return
	}

	category := m.GetModelCategory(modelName)
	now := time.Now().UnixNano() / int64(time.Millisecond)

	m.Lock()
	var acc *Account
	for _, a := range m.accounts {
		if a.ID == id {
			acc = a
			break
		}
	}
	if acc == nil {
		m.Unlock()
		return
	}

	cooldownUntil := int64(0)
	if acc.Cooldowns != nil {
		if v, ok := acc.Cooldowns[category]; ok {
			cooldownUntil = v
		}
	} else {
		cooldownUntil = acc.CooldownUntil
	}
	hasQuota := cooldownUntil == 0 || now >= cooldownUntil

	if !hasQuota {
		m.Unlock()
		return
	}

	currentCount := m.errorCounts[id] + 1
	m.errorCounts[id] = currentCount
	email := acc.Email

	// If threshold reached, clear error count atomically before releasing lock
	shouldFetch := currentCount >= 5
	if shouldFetch {
		delete(m.errorCounts, id) // 清除计数，防止并发多次触发刷新
	}
	m.Unlock()

	if logFn != nil {
		logFn(fmt.Sprintf("⚠️ [负载均衡] 账号 %s 遇到 %d 报错，连续报错次数: %d/5", email, statusCode, currentCount))
	}

	if shouldFetch {
		if logFn != nil {
			logFn(fmt.Sprintf("🔄 [负载均衡] 账号 %s 连续遇到 503/429 达到 5 次，触发自动刷新配额以修正冷静状态...", email))
		}
		if m.FetchQuota != nil {
			go func(a *Account) {
				res, err := m.FetchQuota(a)
				if err == nil && res != nil {
					m.UpdateAccountQuota(a.ID, res)
				} else if err != nil && logFn != nil {
					logFn(fmt.Sprintf("❌ [负载均衡] 账号 %s 自动刷新配额失败: %v", a.Email, err))
				}
			}(acc)
		}
	}
}

func (m *Manager) ResetAccountError(id string) {
	m.Lock()
	defer m.Unlock()
	delete(m.errorCounts, id)
}

func (m *Manager) UpdateAccountQuota(id string, res *QuotaResult) {
	if res == nil {
		return
	}
	m.ResetAccountError(id)
	if len(res.Buckets) > 0 {
		m.UpdateAccountCooldownFromQuota(id, res.Buckets)
	}
	if res.Tier != "" {
		m.UpdateAccountTier(id, res.Tier)
	}
	if res.Credits != nil {
		m.UpdateAccountCredits(id, *res.Credits)
	}
	if m.OnQuotaUpdated != nil {
		m.OnQuotaUpdated(id, res)
	}
}

// ============ Token 同步刷新(单账号互斥 + 双次复用检查) ============

func (m *Manager) RefreshAccountTokenSync(id string) (string, error) {
	acc := m.GetAccountByID(id)
	if acc == nil {
		return "", errors.New("账号未找到")
	}

	if m.RefreshToken == nil {
		return "", errors.New("Token 刷新服务未注册")
	}

	// 1. First Check: 如果 Token 在 30 秒内刚刚成功刷新过，直接复用
	if refreshedAt := acc.GetTokenRefreshedAt(); refreshedAt > 0 && time.Now().Unix()-refreshedAt < 30 {
		if token := acc.GetAccessToken(); token != "" {
			return token, nil
		}
	}

	// 2. 加单账号互斥锁，保障同一账号的并发请求安全串行化
	lock := m.getAccountRefreshLock(id)
	lock.Lock()
	defer lock.Unlock()

	// 3. Second Check: 拿到锁后再次检查，若已被先到达的协程完成刷新则直接复用
	if refreshedAt := acc.GetTokenRefreshedAt(); refreshedAt > 0 && time.Now().Unix()-refreshedAt < 30 {
		if token := acc.GetAccessToken(); token != "" {
			return token, nil
		}
	}

	newToken, err := m.RefreshToken(acc)
	if err != nil {
		return "", err
	}

	m.UpdateAccessToken(id, newToken)
	return newToken, nil
}
