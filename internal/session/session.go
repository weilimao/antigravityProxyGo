package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/fileutil"

	"antigravity-proxy/internal/account"
)

const (
	sessionTTLMS     = 30 * 60 * 1000 // 30 分钟
	gcIntervalMS     = 5 * 60 * 1000  // 5 分钟
	persistFilename  = "session_bindings.json"
	saveDelaySeconds = 3
)

// 包级预编译正则：避免每次请求重复编译，消除 GC 压力
var (
	reAgentReqID       = regexp.MustCompile(`"requestId"\s*:\s*"(?:agent|trajectory|flow)[\\/]+([a-fA-F0-9-]+)`)
	reConvID           = regexp.MustCompile(`"conversationId"\s*:\s*"([a-fA-F0-9-]+)"`)
	reBrainPath        = regexp.MustCompile(`(?:brain|artifacts)[\\/]+([a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})?)`)
	reSessionID        = regexp.MustCompile(`"sessionId"\s*:\s*"?([a-fA-F0-9-]+|-?\d+)"?`)
	reCompanionRequest = regexp.MustCompile(`"requestType"\s*:\s*"(?:agent_monitoring|web_search|command_adjudicator|terminal_classifier)"`)
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

// authKeyHash 把接入凭证 token 压成 "auth:<SHA256[:16hex]>" 形式的会话 baseKey。
// 供 ExtractSessionKey 的 Authorization Bearer 与各凭证头分支复用,保证同一凭证 token 在任意
// 承载形态(Bearer / X-Api-Key / ...)产生同一 auth:<hash>,使 sticky 选号键对凭证粒度稳定。
func authKeyHash(token string) string {
	hasher := sha256.New()
	hasher.Write([]byte(token))
	return "auth:" + hex.EncodeToString(hasher.Sum(nil))[:16]
}

// firstNonShortCredential 按 extractToken(见 relay.compat_translate_helpers)同口径但不含 URL ?key=,
// 依次取承载接入凭证的请求头,返回首个非空且 trim 后长度 >10 的凭证原始值;全空/过短返回 ""。
//
// 取头顺序: X-Api-Key(Anthropic 原生) → X-Goog-Api-Key(Google 风格) → ANTHROPIC_API_KEY / API_KEY
// (分发客户端自定义头)。刻意不含 URL ?key=:会话键按 query 取值会引入"同 URL 不同请求体"的误聚合,
// 与 extractToken 的取值边界在此点刻意分离。长度阈值 >10 为独立门控,过滤极短占位噪声。
// http.Header.Get 大小写不敏感,无需列两种大小写变体(x-goog-api-key 与 X-Goog-Api-Key 同摄)。
func firstNonShortCredential(req *http.Request) string {
	for _, h := range []string{"X-Api-Key", "X-Goog-Api-Key", "ANTHROPIC_API_KEY", "API_KEY"} {
		if tok := strings.TrimSpace(req.Header.Get(h)); len(tok) > 10 {
			return tok
		}
	}
	return ""
}

// ExtractSessionKey 提取会话 Key。优先级: Authorization Bearer > 凭证头(X-Api-Key 等) > Socket 远程端口
func (r *Router) ExtractSessionKey(req *http.Request, reqBody []byte) string {
	baseKey := ""
	authHeader := req.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") && len(authHeader) > 10 {
		baseKey = authKeyHash(authHeader[7:])
	} else if tok := firstNonShortCredential(req); tok != "" {
		// Anthropic SDK / Google 风格 / 分发客户端自定义凭证头兜底:杜绝 X-Api-Key 等凭证鉴权的
		// 客户端被降级到 "sock:<IP>" 兜底,致使 sticky 选号在同一来源 IP 下跨 API Key/用户全部
		// 聚到同一上游账号。产物 "auth:<hash>" 与 Bearer 路径同形态,下游 auth:acc:<hex> 套壳零改动。
		baseKey = authKeyHash(tok)
	} else {
		remoteAddr := req.RemoteAddr
		if remoteAddr == "" {
			remoteAddr = "unknown:0"
		}
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			baseKey = "sock:" + host
		} else {
			baseKey = "sock:" + remoteAddr
		}
	}

	// 从请求体中提取稳定的会话级/周期级特征，作为 sessionKey 的后缀进行更精细的分流
	if len(reqBody) > 0 {
		bodyStr := string(reqBody)

		// 0. 伴生/守护性请求防碎片化：对于后台安全审查、监控、网页检索等伴生调用，不提取临时随机单次 ID，直接返回 baseKey 触发亲和性收敛
		if reCompanionRequest.MatchString(bodyStr) {
			return baseKey
		}

		// 1. 最高优先级：提取显式业务会话/轨迹/智能体 ID (Antigravity/Gemini-CLI 原生规范: agent/<conv-id>/...)
		matchAgent := reAgentReqID.FindStringSubmatch(bodyStr)
		if len(matchAgent) > 1 && !isNumericID(matchAgent[1]) {
			return baseKey + ":" + formatSessionSuffix(matchAgent[1])
		}

		// 2. 次优先提取显式 conversationId
		matchConv := reConvID.FindStringSubmatch(bodyStr)
		if len(matchConv) > 1 && !isNumericID(matchConv[1]) {
			return baseKey + ":" + formatSessionSuffix(matchConv[1])
		}

		// 3. 第三优先：提取本地工作区 brain / artifacts 路径中的会话 UUID (Antigravity 本地工作区特征)
		matchBrain := reBrainPath.FindStringSubmatch(bodyStr)
		if len(matchBrain) > 1 && !isNumericID(matchBrain[1]) {
			return baseKey + ":" + formatSessionSuffix(matchBrain[1])
		}

		// 4. 第四优先：提取显式会话 sessionId (严格过滤客户端全局长整数/负数遥测编号，如 -3750763034362895579)
		matchSession := reSessionID.FindStringSubmatch(bodyStr)
		if len(matchSession) > 1 && !isNumericID(matchSession[1]) {
			return baseKey + ":" + formatSessionSuffix(matchSession[1])
		}

		// 5. 后备 JSON 反序列化解析双保险机制 (仅对稳定会话特征作提取，安全丢弃单次随机的临时 requestId)
		var temp struct {
			SessionID      interface{} `json:"sessionId"`
			ConversationID string      `json:"conversationId"`
			RequestID      string      `json:"requestId"`
		}
		if err := json.Unmarshal(reqBody, &temp); err == nil {
			// (a) 优先解析并过滤带有前缀的轨迹级 requestId
			if temp.RequestID != "" {
				for _, prefix := range []string{"agent/", "trajectory/", "flow/"} {
					if strings.HasPrefix(temp.RequestID, prefix) {
						subParts := strings.Split(temp.RequestID, "/")
						if len(subParts) > 1 && !isNumericID(subParts[1]) {
							return baseKey + ":" + formatSessionSuffix(subParts[1])
						}
					}
				}
			}

			// (b) 其次解析 conversationId
			if temp.ConversationID != "" && !isNumericID(temp.ConversationID) {
				return baseKey + ":" + formatSessionSuffix(temp.ConversationID)
			}

			// (c) 最后解析并转换 sessionId (同样过滤纯数字/静态长整数)
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
				if valStr != "" && !isNumericID(valStr) {
					return baseKey + ":" + formatSessionSuffix(valStr)
				}
			}
		}
	}

	return baseKey
}

// formatSessionSuffix 格式化会话特征后缀（去除连字符并截取前 8 位）
func formatSessionSuffix(raw string) string {
	val := strings.ReplaceAll(raw, "-", "")
	if len(val) >= 8 {
		return val[:8]
	}
	return val
}

// isNumericID 检测标识串是否为纯数字/长整数(含正负号)，用于识别与过滤 Google 静态客户端级遥测 ID
func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ExtractClientSessionHeader 从入站请求头识别「客户端原生会话标识」,供全链路四号池统一
// sticky 选号键与 OCR 缓存隔离键复用。与 ExtractSessionKey 互补:后者只看 Authorization/
// sock/body,前者专看客户端在请求头里显式携带的会话 UUID。两者并列,由调用方决定优先级
// (见 relay.session_key.ensureSessionKey:本方法命中优先,未命中再回退 ExtractSessionKey)。
//
// 识别优先级(整段 UUID 落地,跨进程重启稳定可对照,与 X-Claude-Code-Session-Id 同口径):
//  1. X-Claude-Code-Session-Id  → "claude:<UUID>"  (Claude Code CLI/VSCode 原生会话头)
//  2. X-Session-Id              → "<客户端前缀>:<UUID>" (OpenCode/ZCode 共用此头,前缀按 UA 区分)
//  3. X-Session-Affinity        → 同上 (OpenCode/ZCode 会话亲和性头,兜底同 X-Session-Id)
//  4. Session-Id                → "codex:<UUID>"   (Codex TUI 原生会话头,与 Thread-Id 等值)
//  5. Thread-Id                 → "codex:<UUID>"   (Codex 会话线程头,兜底同 Session-Id)
// 任一命中即返回对应前缀的整段值;全部缺失/纯空白返回空串,调用方据此回退 ExtractSessionKey。
//
// 不改 ExtractSessionKey 主体:其 auth/sock/body 兜底口径为既有非客户端头路径(脚本/SDK 直调)
// 的稳定契约,本方法为纯加法,仅在客户端显式携带会话头时短路到更细粒度的会话键。
func (r *Router) ExtractClientSessionHeader(req *http.Request) string {
	if req == nil {
		return ""
	}
	if sid := strings.TrimSpace(req.Header.Get("X-Claude-Code-Session-Id")); sid != "" {
		return "claude:" + sid
	}
	if sid := strings.TrimSpace(req.Header.Get("X-Session-Id")); sid != "" {
		return sessionHeaderClientPrefix(req.Header.Get("User-Agent")) + sid
	}
	if sid := strings.TrimSpace(req.Header.Get("X-Session-Affinity")); sid != "" {
		return sessionHeaderClientPrefix(req.Header.Get("User-Agent")) + sid
	}
	if sid := strings.TrimSpace(req.Header.Get("Session-Id")); sid != "" {
		return "codex:" + sid
	}
	if tid := strings.TrimSpace(req.Header.Get("Thread-Id")); tid != "" {
		return "codex:" + tid
	}
	return ""
}

// sessionHeaderClientPrefix 按 User-Agent 区分 X-Session-Id / X-Session-Affinity 的来源客户端。
// 历史实现一律标 "opencode:"(该约定随 OpenCode 接入时写死),ZCode 等同样携带这些头的
// 客户端会被误标成 opencode;现按 UA 细分,ZCode(UA 形如 ZCode/x.y.z)标 "zcode:",
// 识别不出保持历史 "opencode:" 兜底,避免既有“opencode:”前缀键的粘贴粘性会话断层。
func sessionHeaderClientPrefix(ua string) string {
	u := strings.ToLower(strings.TrimSpace(ua))
	switch {
	case strings.Contains(u, "zcode"):
		return "zcode:"
	case strings.Contains(u, "opencode"):
		return "opencode:"
	default:
		return "opencode:"
	}
}

// parseBaseSessionKey 分解会话键，分离出客户端根基键(baseKey)与业务子后缀(subSuffix)。
// 格式规则示例:
//   "auth:acc:<hex16>:<subId>" -> baseKey="auth:acc:<hex16>", subSuffix="<subId>"
//   "auth:acc:<hex16>"         -> baseKey="auth:acc:<hex16>", subSuffix=""
//   "sock:acc:<ip>:<subId>"    -> baseKey="sock:acc:<ip>",    subSuffix="<subId>"
//   "sock:acc:<ip>"            -> baseKey="sock:acc:<ip>",    subSuffix=""
//   "acc:<id>:<subId>"         -> baseKey="acc:<id>",         subSuffix="<subId>"
//   "acc:<id>"                 -> baseKey="acc:<id>",         subSuffix=""
func parseBaseSessionKey(sessionKey string) (baseKey string, subSuffix string) {
	parts := strings.Split(sessionKey, ":")
	if strings.HasPrefix(sessionKey, "auth:acc:") || strings.HasPrefix(sessionKey, "auth:prj:") ||
		strings.HasPrefix(sessionKey, "sock:acc:") || strings.HasPrefix(sessionKey, "sock:prj:") {
		if len(parts) >= 4 {
			baseKey = strings.Join(parts[:3], ":")
			subSuffix = strings.Join(parts[3:], ":")
			return baseKey, subSuffix
		}
		return sessionKey, ""
	}
	if strings.HasPrefix(sessionKey, "acc:") || strings.HasPrefix(sessionKey, "prj:") {
		if len(parts) >= 3 {
			baseKey = strings.Join(parts[:2], ":")
			subSuffix = strings.Join(parts[2:], ":")
			return baseKey, subSuffix
		}
		return sessionKey, ""
	}
	if idx := strings.LastIndex(sessionKey, ":"); idx != -1 {
		return sessionKey[:idx], sessionKey[idx+1:]
	}
	return sessionKey, ""
}

// getActiveSiblingAccountLocked 必须在调用方持有读锁或写锁的前提下调用。
// 查找属于 baseKey + ":" 命名空间下且未超时的最新活跃子会话账号。
func (r *Router) getActiveSiblingAccountLocked(baseKey string, availableAccounts []*account.Account, now int64) *account.Account {
	if baseKey == "" || len(availableAccounts) == 0 {
		return nil
	}

	availMap := make(map[string]*account.Account, len(availableAccounts))
	for _, a := range availableAccounts {
		availMap[a.ID] = a
	}

	var latestAccount *account.Account
	var latestTime int64

	childPrefix := baseKey + ":"
	for key, entry := range r.sessionMap {
		if strings.HasPrefix(key, childPrefix) {
			if now-entry.LastActive < sessionTTLMS {
				if acc, ok := availMap[entry.AccountID]; ok {
					if entry.LastActive > latestTime {
						latestTime = entry.LastActive
						latestAccount = acc
					}
				}
			}
		}
	}
	return latestAccount
}

func (r *Router) GetOrAssignAccount(sessionKey string, availableAccounts []*account.Account, logFn func(string)) *account.Account {
	if len(availableAccounts) == 0 {
		return nil
	}

	now := time.Now().UnixNano() / int64(time.Millisecond)
	baseKey, subSuffix := parseBaseSessionKey(sessionKey)

	// ── 场景 A: 伴生/守护请求 (subSuffix 为空) ────────────────────────────
	// 针对 gemini-3.5-flash-lite、agent_monitoring、terminal check 等无会话后缀的伴随调用：
	// 动态收敛至同客户端 (baseKey) 下当前活跃的主任务会话账号，且绝不持久化写入 sessionMap[baseKey]，
	// 既防止跨号审查代码的指纹风险，又彻底避免反向污染 baseKey 导致后续新会话被薅单号。
	if subSuffix == "" {
		r.RLock()
		siblingAccount := r.getActiveSiblingAccountLocked(baseKey, availableAccounts, now)
		_, hasSelf := r.sessionMap[sessionKey]
		r.RUnlock()

		if siblingAccount != nil {
			if hasSelf {
				r.Lock()
				delete(r.sessionMap, sessionKey)
				r.Unlock()
				r.scheduleSave()
			}
			if logFn != nil {
				logFn(fmt.Sprintf("🔒 [伴生收敛] 伴生审查请求 %s 动态收敛至当前活跃对话账号: %s", sessionKey, siblingAccount.Email))
			}
			return siblingAccount
		}
	}

	// ── 场景 B: 独立业务会话或常规请求 ──────────────────────────────────
	// 1. Fast path: 既有会话粘性命中检查 (read-lock)
	r.RLock()
	existing, found := r.sessionMap[sessionKey]
	r.RUnlock()

	if found {
		for _, a := range availableAccounts {
			if a.ID == existing.AccountID {
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
		if logFn != nil {
			logFn(fmt.Sprintf("🔄 [粘性路由] 会话 %s 原绑定账号不可用，重新分配...", sessionKey))
		}
	}

	// 2. Slow path: 写锁内分配新账号 (纯正负载均衡，空闲账号优先打散)
	r.Lock()
	defer r.Unlock()

	// Double-check inside write lock
	if subSuffix == "" {
		if siblingAccount := r.getActiveSiblingAccountLocked(baseKey, availableAccounts, now); siblingAccount != nil {
			if logFn != nil {
				logFn(fmt.Sprintf("🔒 [伴生收敛] 伴生审查请求 %s 动态收敛至当前活跃对话账号: %s", sessionKey, siblingAccount.Email))
			}
			return siblingAccount
		}
	}

	if existing2, found2 := r.sessionMap[sessionKey]; found2 {
		for _, a := range availableAccounts {
			if a.ID == existing2.AccountID {
				r.sessionMap[sessionKey] = SessionEntry{AccountID: existing2.AccountID, LastActive: now}
				r.scheduleSave()
				return a
			}
		}
		delete(r.sessionMap, sessionKey)
	}

	// Strategy 1: prefer idle accounts (打散分配给空闲账号)
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

	hasChildren := make(map[string]bool)
	for key := range parsed {
		base, sub := parseBaseSessionKey(key)
		if sub != "" {
			hasChildren[base] = true
		}
	}

	now := time.Now().UnixNano() / int64(time.Millisecond)
	loaded := 0
	r.sessionMap = make(map[string]SessionEntry)
	for key, entry := range parsed {
		// 历史脏数据自愈：若同客户端已存在明确的子会话，过滤无会话后缀的冗余 bare baseKey 记录
		_, sub := parseBaseSessionKey(key)
		if sub == "" && hasChildren[key] {
			continue
		}
		if now-entry.LastActive <= sessionTTLMS {
			r.sessionMap[key] = entry
			loaded++
		}
	}
	fmt.Printf("[SessionRouter] Loaded %d valid session bindings from disk\n", loaded)
}

func (r *Router) SaveToDisk() {
	// 深拷贝:读锁内先取会话 map,Marshal 时不还依赖写锁/读锁,写盘阶段与并发会话请求
	// 不再共享同一底层数据,消除"marshal/原子写期间阻塞路由" 的旧卡顿。
	r.RLock()
	path := r.persistPath
	if path == "" {
		r.RUnlock()
		return
	}
	data := make(map[string]SessionEntry, len(r.sessionMap))
	for k, v := range r.sessionMap {
		data[k] = v
	}
	r.RUnlock()

	// Marshal 而非 MarshalIndent:会话绑定数量小,机器化序列化在写盘上 CPU/体积更优。
	bytesData, err := json.Marshal(data)
	if err != nil {
		return
	}

	// 原子写:磁盘写满不再把会话绑定文件截成半截(老 os.WriteFile 的截断事故源)。
	if err := fileutil.WriteFileAtomic(path, bytesData, 0644); err != nil {
		fmt.Printf("[SessionRouter] 落盘失败: %v\n", err)
	}
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

