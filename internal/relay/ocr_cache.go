package relay

// ocr_cache.go —— 入站 image 自愈降级 OCR 结果的进程内缓存。
//
// 背景:downgradeAnthropicImagesToText / dispatchToGemini 自愈链路对每张 base64 图
// 调 ocrImageViaLocalGemini 让本地 gemini-2.5-flash 跑 OCR。Claude Code 客户端无状态,
// 每轮重发完整历史,导致同一张历史图被重复 OCR N 次,每次烧一次 antigravity 号池额度
// 并加 ~3s 延迟。本缓存以 (UserKey, ocrModel, sha256(b64)) 为键做进程内 LRU + singleflight:
//   - 命中即返回历史 OCR 文本,跳过 gemini 调用与 ~3s 延迟;
//   - 切换 OCR 模型后键变化,自动重新 OCR 一次新模型(配置改了立刻生效);
//   - 同图并发相邻请求由 singleflight 合并为 1 次真上游调用,防缓存击穿;
//   - OCR 失败也缓存(短 TTL),熔断窗口内不再重打挂的 OCR 服务,避免雪崩;
//   - LRU 限条数上限,防长跑进程无限增长。
//
// 不持久化:进程内易失,重启重新 OCR 一次可接受(图本就是历史一次性印证)。
// 不引入磁盘 I/O 与文件锁复杂度。缓存字段挂在 APICompatHandler 上,生命周期同进程。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"antigravity-proxy/internal/db"

	lru "github.com/hashicorp/golang-lru/v2"
)

// ocrCacheEntry 是单张图的 OCR 缓存条目。
// text: OCR 识别出的纯文本(成功)或失败时的空串(配合调用方走占位文本兜底)。
// err:  nil=成功;text 为空串时 err 记录失败原因,供命中失败条目返回时维持旧行为。
// ok:   true=OCR 成功(长 TTL);false=失败(短 TTL,熔断窗口避免重打挂的 OCR 服务)。
// expiresAt: 该条目的过期时刻(TTL 由 ok 决定),过期在 Get 时点算并主动淘汰。
type ocrCacheEntry struct {
	text      string
	err       error
	ok        bool
	expiresAt time.Time
}

// ocrLRUCache 是 OCR 结果的进程内 + SQLite 磁盘持久化缓存。
// 键: ownerKey|ocrModel|sha256(b64)[:16],按用户/会话与 OCR 模型双重隔离,不含提问文本。
type ocrLRUCache struct {
	mu     sync.Mutex
	lru    *lru.Cache[string, ocrCacheEntry]
	ttlOk  time.Duration // OCR 成功条目 TTL,默认 24 小时
	ttlBad time.Duration // OCR 失败条目 TTL(熔断窗口),默认 30 秒
}

// newOcrLRUCache 构造一个 OCR 缓存。capacity<=0 用默认 256;ttl<=0 用默认 24h/30s。
//
// 成功 TTL 取 24h(而非更短)的原因:Claude Code 客户端无状态,每轮重发完整历史,
// 历史 image 块会被反复带过来。若成功 TTL 太短(如 30min),用户隔半天/隔夜回来追问,
// 历史图全部过期 → 全部重新 OCR,白烧一次 antigravity 号池配额 + 每张 ~3s 延迟,
// 与本缓存的初衷(消灭"每轮重打")相悖。24h 覆盖一个完整工作日的追问窗口,
// 真正防无限增长交给 LRU 容量上限淘汰,而非靠 TTL 硬删——LRU 按最近访问淘汰,
// 越常用越不容易被挤掉,比 TTL 一刀切更合理。失败 TTL 仍 30s 熔断窗口不变。
func newOcrLRUCache(capacity int, ttlOk, ttlBad time.Duration) *ocrLRUCache {
	if capacity <= 0 {
		capacity = 256
	}
	if ttlOk <= 0 {
		ttlOk = 24 * time.Hour
	}
	if ttlBad <= 0 {
		ttlBad = 30 * time.Second
	}
	c, _ := lru.New[string, ocrCacheEntry](capacity)
	return &ocrLRUCache{lru: c, ttlOk: ttlOk, ttlBad: ttlBad}
}

// ocrCacheKey 返回 ownerKey + ocrModel + 图指纹 三维隔离的缓存键。
// b64Data 可能数 MB,不直接做键,先算 sha256 取前 16 字节再 hex(定长 32 字符)。
// ocrModel 纳入键:切换 OCR 模型后不命中旧模型结果,自动重新 OCR 新模型,
// 保证前端改模型立刻生效、不触达旧缓存(否则配置形同虚设)。
//
// 不含 promptCtx(用户提问文本):同一张图在同一会话+同一 OCR 模型下只 OCR 一次,
// 用户提问文本变化(含"继续"/resume/标点微调等无实质意图的变化)不触发重 OCR,省配额、省 ~3s 延迟。
// OCR 的"靶向分析"由 ocrImageUncached 在真打 gemini 上游时用 promptCtx 组 ocrPrompt 承担,
// 与缓存键解耦——缓存按图片身份命中,OCR 按当前提问靶向分析,二者各司其职。
// 历史背景:曾把 promptCtx 哈希作为第四维键以"不同提问命中不同靶向 OCR 结果",但 Claude Code
// 无状态客户端每轮重发历史时,微小文本变化(如 resume 标记)会让同图反复 miss 重打 gemini,
// 消耗 antigravity 号池配额且体感延迟。现剥离回三维,靶向性由完整上下文中的上游 LLM 弥补。
func ocrCacheKey(userKey, ocrModel, b64Data string) string {
	m := strings.TrimSpace(ocrModel)
	if m == "" {
		m = defaultOcrModel
	}
	sum := sha256.Sum256([]byte(b64Data))
	return userKey + "|" + m + "|" + hex.EncodeToString(sum[:])[:16]
}

// get 命中且未过期返回 entry 与 true;若内存 miss 则回退查询 SQLite 持久化表并回填内存。
func (c *ocrLRUCache) get(key string) (ocrCacheEntry, bool) {
	c.mu.Lock()
	e, ok := c.lru.Get(key)
	if ok {
		if time.Now().After(e.expiresAt) {
			c.lru.Remove(key)
			c.mu.Unlock()
			return ocrCacheEntry{}, false
		}
		c.mu.Unlock()
		return e, true
	}
	c.mu.Unlock()

	// 内存未命中(如程序重启后)，检查 SQLite 数据库持久化缓存 (24h TTL)
	if text, okDB := db.GetOcrCache(key); okDB && strings.TrimSpace(text) != "" {
		entry := ocrCacheEntry{
			text:      text,
			err:       nil,
			ok:        true,
			expiresAt: time.Now().Add(c.ttlOk),
		}
		c.mu.Lock()
		c.lru.Add(key, entry)
		c.mu.Unlock()
		return entry, true
	}

	return ocrCacheEntry{}, false
}

// set 写入一条缓存,根据 ok 选 TTL(成功长 TTL / 失败短 TTL 熔断窗口),成功识别条目写盘持久化。
func (c *ocrLRUCache) set(key string, text string, err error, ok bool) {
	c.mu.Lock()
	ttl := c.ttlOk
	if !ok {
		ttl = c.ttlBad
	}
	expiresAt := time.Now().Add(ttl)
	c.lru.Add(key, ocrCacheEntry{
		text:      text,
		err:       err,
		ok:        ok,
		expiresAt: expiresAt,
	})
	c.mu.Unlock()

	// 识别成功的图片 OCR 文本写盘存入 SQLite 数据库
	if ok && strings.TrimSpace(text) != "" {
		go func() {
			_ = db.SaveOcrCache(key, text, expiresAt)
		}()
	}
}

// ocrCacheCounters 统计缓存命中/未命中,供日志显示降级收益。计数器走 atomic,
// 不进 LRU 锁临界区,避免热路径争用。
type ocrCacheCounters struct {
	hits   atomic.Int64
	misses atomic.Int64
}

func (cc *ocrCacheCounters) snapshot() (hits, misses int64) {
	return cc.hits.Load(), cc.misses.Load()
}
