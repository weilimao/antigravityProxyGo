package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
)

const (
	sessionTTLMS     = 30 * 60 * 1000 // 30 分钟
	gcIntervalMS     = 5 * 60 * 1000  // 5 分钟
	persistFilename  = "session_bindings.json"
	saveDelaySeconds = 3
)

type SessionEntry struct {
	AccountID  string `json:"accountId"`
	LastActive int64  `json:"lastActive"`
}

type Router struct {
	sync.RWMutex
	sessionMap   map[string]SessionEntry
	persistPath  string
	saveTimer    *time.Timer
	saveTimerMu  sync.Mutex
	gcStop       chan struct{}
	
	// 事件回调
	OnSessionsCleared func()
}

func NewRouter() *Router {
	return &Router{
		sessionMap: make(map[string]SessionEntry),
	}
}

func (r *Router) Init(dataDir string) {
	r.Lock()
	r.persistPath = filepath.Join(dataDir, persistFilename)
	r.Unlock()

	r.LoadFromDisk()
	r.StartGC()
}

func (r *Router) UpdatePath(newDataDir string) {
	r.SaveToDisk()

	r.Lock()
	r.persistPath = filepath.Join(newDataDir, persistFilename)
	r.Unlock()

	r.LoadFromDisk()
}

// ExtractSessionKey 提取会话 Key。优先级：Authorization Token > Socket 远程端口
func (r *Router) ExtractSessionKey(req *http.Request, reqBody []byte) string {
	baseKey := ""
	authHeader := req.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") && len(authHeader) > 10 {
		token := authHeader[7:]
		hasher := sha256.New()
		hasher.Write([]byte(token))
		hashHex := hex.EncodeToString(hasher.Sum(nil))
		baseKey = "auth:" + hashHex[:16]
	} else {
		remoteAddr := req.RemoteAddr
		if remoteAddr == "" {
			remoteAddr = "unknown:0"
		}
		baseKey = "sock:" + remoteAddr
	}

	// 从请求体中提取稳定的会话级/周期级特征，作为 sessionKey 的后缀进行更精细的分流
	if len(reqBody) > 0 {
		bodyStr := string(reqBody)

		// 1. 优先提取显式会话 sessionId (支持带或不带双引号的 UUID 或大整数 ID)
		reSession := regexp.MustCompile(`"sessionId"\s*:\s*"?(a-fA-F0-9-]+|-?\d+)"?`)
		matchSession := reSession.FindStringSubmatch(bodyStr)
		if len(matchSession) > 1 {
			val := strings.ReplaceAll(matchSession[1], "-", "")
			if len(val) >= 8 {
				return baseKey + ":" + val[:8]
			}
			return baseKey + ":" + val
		}

		// 2. 次优先提取 conversationId 会话 ID
		reConv := regexp.MustCompile(`"conversationId"\s*:\s*"([a-fA-F0-9-]+)"`)
		matchConv := reConv.FindStringSubmatch(bodyStr)
		if len(matchConv) > 1 {
			val := strings.ReplaceAll(matchConv[1], "-", "")
			if len(val) >= 8 {
				return baseKey + ":" + val[:8]
			}
			return baseKey + ":" + val
		}

		// 3. 仅在含有特定前缀 (agent/ 或 trajectory/ 或 flow/) 时，才提取轨迹级稳定的 requestId
		reAgentReq := regexp.MustCompile(`"requestId"\s*:\s*"(?:agent|trajectory|flow)\/([a-fA-F0-9-]+)`)
		matchAgent := reAgentReq.FindStringSubmatch(bodyStr)
		if len(matchAgent) > 1 {
			val := strings.ReplaceAll(matchAgent[1], "-", "")
			if len(val) >= 8 {
				return baseKey + ":" + val[:8]
			}
			return baseKey + ":" + val
		}

		// 4. 后备 JSON 反序列化解析双保险机制 (仅对稳定会话特征作提取，安全丢弃单次随机的临时 requestId)
		var temp struct {
			SessionID      interface{} `json:"sessionId"`
			ConversationID string      `json:"conversationId"`
			RequestID      string      `json:"requestId"`
		}
		if err := json.Unmarshal(reqBody, &temp); err == nil {
			// (a) 解析并转换 sessionId
			if temp.SessionID != nil {
				var valStr string
				switch v := temp.SessionID.(type) {
				case string:
					valStr = v
				case float64:
					valStr = fmt.Sprintf("%.0f", v)
				case int64:
					valStr = fmt.Sprintf("%d", v)
				}
				if valStr != "" {
					val := strings.ReplaceAll(valStr, "-", "")
					if len(val) >= 8 {
						return baseKey + ":" + val[:8]
					}
					return baseKey + ":" + val
				}
			}

			// (b) 解析 conversationId
			if temp.ConversationID != "" {
				val := strings.ReplaceAll(temp.ConversationID, "-", "")
				if len(val) >= 8 {
					return baseKey + ":" + val[:8]
				}
				return baseKey + ":" + val
			}

			// (c) 解析并过滤带有前缀的轨迹级 requestId
			if temp.RequestID != "" {
				for _, prefix := range []string{"agent/", "trajectory/", "flow/"} {
					if strings.HasPrefix(temp.RequestID, prefix) {
						subParts := strings.Split(temp.RequestID, "/")
						if len(subParts) > 1 {
							uuid := subParts[1]
							val := strings.ReplaceAll(uuid, "-", "")
							if len(val) >= 8 {
								return baseKey + ":" + val[:8]
							}
							return baseKey + ":" + val
						}
					}
				}
			}
		}
	}

	return baseKey
}

func (r *Router) GetOrAssignAccount(sessionKey string, availableAccounts []*account.Account, logFn func(string)) *account.Account {
	if len(availableAccounts) == 0 {
		return nil
	}

	now := time.Now().UnixNano() / int64(time.Millisecond)

	// ── Fast path: read-lock only ──────────────────────────────────────────
	// In the vast majority of requests the session is already bound, so we
	// try a cheap RLock first to avoid serialising every goroutine behind a
	// global write lock.
	r.RLock()
	existing, found := r.sessionMap[sessionKey]
	r.RUnlock()

	if found {
		// Validate the bound account still exists in the available pool
		for _, a := range availableAccounts {
			if a.ID == existing.AccountID {
				// Refresh last-active timestamp – needs a write lock, but only
				// for this tiny update so contention is minimal.
				r.Lock()
				r.sessionMap[sessionKey] = SessionEntry{
					AccountID:  existing.AccountID,
					LastActive: now,
				}
				r.Unlock()
				r.scheduleSave()
				if logFn != nil {
					logFn(fmt.Sprintf("🔒 [粘性路由] 会话 %s 命中已分配账号: %s", sessionKey, a.Email))
				}
				return a
			}
		}
		// Bound account no longer available – fall through to re-assign
		if logFn != nil {
			logFn(fmt.Sprintf("🔄 [粘性路由] 会话 %s 原绑定账号不可用，重新分配...", sessionKey))
		}
	}

	// ── Slow path: write-lock for new assignment ───────────────────────────
	r.Lock()
	defer r.Unlock()

	// Re-check inside the write lock to avoid TOCTOU race where two goroutines
	// both missed the fast path and are racing to assign for the same key.
	if existing2, found2 := r.sessionMap[sessionKey]; found2 {
		for _, a := range availableAccounts {
			if a.ID == existing2.AccountID {
				r.sessionMap[sessionKey] = SessionEntry{AccountID: existing2.AccountID, LastActive: now}
				r.scheduleSave()
				return a
			}
		}
		// Still invalid – clear stale entry before assigning
		delete(r.sessionMap, sessionKey)
	}

	// Strategy 1: prefer idle accounts
	boundAccountIDs := make(map[string]bool, len(r.sessionMap))
	for _, entry := range r.sessionMap {
		boundAccountIDs[entry.AccountID] = true
	}

	var idleAccounts []*account.Account
	for _, a := range availableAccounts {
		if !boundAccountIDs[a.ID] {
			idleAccounts = append(idleAccounts, a)
		}
	}

	var assigned *account.Account
	if len(idleAccounts) > 0 {
		index := r.hashToIndex(sessionKey, len(idleAccounts))
		assigned = idleAccounts[index]
		if logFn != nil {
			logFn(fmt.Sprintf("🆕 [粘性路由] 会话 %s 分配至空闲账号: %s (空闲 %d/%d)", sessionKey, assigned.Email, len(idleAccounts), len(availableAccounts)))
		}
	} else {
		// Strategy 2: consistent hash scatter
		index := r.hashToIndex(sessionKey, len(availableAccounts))
		assigned = availableAccounts[index]
		if logFn != nil {
			logFn(fmt.Sprintf("🆕 [粘性路由] 会话 %s 哈希分配账号: %s (所有账号均已绑定)", sessionKey, assigned.Email))
		}
	}

	r.sessionMap[sessionKey] = SessionEntry{
		AccountID:  assigned.ID,
		LastActive: now,
	}
	r.scheduleSave()

	return assigned
}

func (r *Router) ClearAllAndSave() int {
	r.Lock()
	count := len(r.sessionMap)
	r.sessionMap = make(map[string]SessionEntry)
	r.Unlock()

	r.SaveToDisk()
	if r.OnSessionsCleared != nil {
		r.OnSessionsCleared()
	}
	return count
}

func (r *Router) InvalidateByAccountId(accountID string) int {
	r.Lock()
	count := 0
	for key, entry := range r.sessionMap {
		if entry.AccountID == accountID {
			delete(r.sessionMap, key)
			count++
		}
	}
	r.Unlock()

	if count > 0 {
		r.SaveToDisk()
	}
	return count
}

func (r *Router) GetSessionCount() int {
	r.RLock()
	defer r.RUnlock()
	return len(r.sessionMap)
}

func (r *Router) hashToIndex(key string, length int) int {
	var hash uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash = hash * 16777619
	}
	return int(hash % uint32(length))
}

func (r *Router) LoadFromDisk() {
	r.Lock()
	defer r.Unlock()

	if r.persistPath == "" {
		return
	}

	if _, err := os.Stat(r.persistPath); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(r.persistPath)
	if err != nil {
		fmt.Printf("[SessionRouter] Failed to read session bindings file: %v\n", err)
		return
	}

	var parsed map[string]SessionEntry
	if err := json.Unmarshal(data, &parsed); err != nil {
		fmt.Printf("[SessionRouter] Failed to parse session bindings json: %v\n", err)
		return
	}

	now := time.Now().UnixNano() / int64(time.Millisecond)
	loaded := 0
	r.sessionMap = make(map[string]SessionEntry)
	for key, entry := range parsed {
		if now-entry.LastActive <= sessionTTLMS {
			r.sessionMap[key] = entry
			loaded++
		}
	}
	fmt.Printf("[SessionRouter] Loaded %d valid session bindings from disk\n", loaded)
}

func (r *Router) SaveToDisk() {
	r.Lock()
	if r.persistPath == "" {
		r.Unlock()
		return
	}
	data, err := json.MarshalIndent(r.sessionMap, "", "  ")
	r.Unlock()

	if err != nil {
		return
	}

	r.Lock()
	_ = os.WriteFile(r.persistPath, data, 0644)
	r.Unlock()
}

func (r *Router) scheduleSave() {
	r.saveTimerMu.Lock()
	defer r.saveTimerMu.Unlock()

	if r.saveTimer != nil {
		return
	}

	r.saveTimer = time.AfterFunc(saveDelaySeconds*time.Second, func() {
		r.SaveToDisk()
		r.saveTimerMu.Lock()
		r.saveTimer = nil
		r.saveTimerMu.Unlock()
	})
}

func (r *Router) StartGC() {
	r.Lock()
	if r.gcStop != nil {
		r.Unlock()
		return
	}
	r.gcStop = make(chan struct{})
	r.Unlock()

	go func() {
		ticker := time.NewTicker(gcIntervalMS * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.Lock()
				now := time.Now().UnixNano() / int64(time.Millisecond)
				removed := 0
				for key, entry := range r.sessionMap {
					if now-entry.LastActive > sessionTTLMS {
						delete(r.sessionMap, key)
						removed++
					}
				}
				r.Unlock()

				if removed > 0 {
					fmt.Printf("[SessionRouter] GC: Cleaned %d expired sessions, remaining: %d\n", removed, r.GetSessionCount())
					r.SaveToDisk()
				}
			case <-r.gcStop:
				return
			}
		}
	}()
}

func (r *Router) StopGC() {
	r.Lock()
	defer r.Unlock()
	if r.gcStop != nil {
		close(r.gcStop)
		r.gcStop = nil
	}
}

// SessionBindingInfo 统一返回的绑定数据结构
type SessionBindingInfo struct {
	SessionKey string `json:"sessionKey"`
	AccountID  string `json:"accountId"`
	LastActive int64  `json:"lastActive"`
}

// GetBindings 获取当前所有的会话绑定快照
func (r *Router) GetBindings() []SessionBindingInfo {
	r.RLock()
	defer r.RUnlock()
	res := make([]SessionBindingInfo, 0, len(r.sessionMap))
	for k, v := range r.sessionMap {
		res = append(res, SessionBindingInfo{
			SessionKey: k,
			AccountID:  v.AccountID,
			LastActive: v.LastActive,
		})
	}
	return res
}

// UnbindSession 解绑单个指定会话
func (r *Router) UnbindSession(sessionKey string) bool {
	r.Lock()
	_, found := r.sessionMap[sessionKey]
	if found {
		delete(r.sessionMap, sessionKey)
	}
	r.Unlock()
	if found {
		r.SaveToDisk()
	}
	return found
}

