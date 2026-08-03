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
//   - RefreshAccountTokenSync 用 getAccountRefreshLock 做单账号互斥 + 双次 30s 复用检查。

// ============ 冷却监控(2 分钟 tick) ============

func (m *Manager) StartCooldownMonitor() {
	m.Lock()
	if m.cooldownTicker != nil {
		m.Unlock()
		return
	}
	m.cooldownTicker = time.NewTicker(2 * time.Minute)
	m.cooldownStop = make(chan struct{})
	m.Unlock()

	go func() {
		for {
			select {
			case <-m.cooldownTicker.C:
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
					continue
				}

				if m.FetchQuota == nil {
					// 如果未注册配额拉取回调，直接解除冷静状态
					m.Lock()
					for _, acc := range cooldownAccounts {
						acc.CooldownUntil = 0
						acc.Cooldowns = make(map[string]int64)
					}
					m.Unlock()
					_ = m.SaveAccounts(false)
					continue
				}

				for _, acc := range cooldownAccounts {
					// 异步刷新验证
					go func(a *Account) {
						fmt.Printf("[CooldownMonitor] Verifying quota for cooled account: %s\n", a.Email)
						res, err := m.FetchQuota(a)
						if err != nil {
							// 刷新失败，冷静期往后延长 5 分钟
							m.Lock()
							targetAcc := m.GetAccountByID(a.ID)
							if targetAcc != nil {
								nextCooldown := time.Now().UnixNano()/int64(time.Millisecond) + 5*60*1000
								targetAcc.CooldownUntil = nextCooldown
								if targetAcc.Cooldowns != nil {
									for k := range targetAcc.Cooldowns {
										targetAcc.Cooldowns[k] = nextCooldown
									}
								}
							}
							m.Unlock()
							_ = m.SaveAccounts(true)
							return
						}

						if res != nil {
							m.UpdateAccountQuota(a.ID, res)
						}
					}(acc)
				}
			case <-m.cooldownStop:
				return
			}
		}
	}()
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
	m.Unlock()

	go func() {
		for {
			select {
			case <-m.tokenRefreshTicker.C:
				m.CheckAndRefreshTokens()
			case <-m.tokenRefreshStop:
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

	for _, acc := range refreshAccounts {
		go func(a *Account) {
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
	m.RLock()
	acc := m.GetAccountByID(id)
	m.RUnlock()
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
