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

// ocrLRUCache 是 OCR 结果的进程内缓存。
// 键: UserKey|ocrModel|sha256(b64)[:16],按用户与 OCR 模型双重隔离。
type ocrLRUCache struct {
	mu     sync.Mutex
	lru    *lru.Cache[string, ocrCacheEntry]
	ttlOk  time.Duration // OCR 成功条目 TTL,默认 30 分钟
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

// ocrCacheKey 返回 UserKey + ocrModel + userPromptText(可选) 多维隔离的缓存键。
// b64Data 可能数 MB,不直接做键,先算 sha256 取前 16 字节再 hex(定长 32 字符)。
// ocrModel 纳入键:切换 OCR 模型后不命中旧模型结果,自动重新 OCR 新模型,
// 保证前端改模型立刻生效、不触达旧缓存(否则配置形同虚设)。
// userPromptText(可选):用户附带的提问文本哈希隔离,不同提问命中针对该提问的靶向 OCR 结果。
func ocrCacheKey(userKey, ocrModel, b64Data string, userPromptText ...string) string {
	m := strings.TrimSpace(ocrModel)
	if m == "" {
		m = defaultOcrModel
	}
	sum := sha256.Sum256([]byte(b64Data))
	key := userKey + "|" + m + "|" + hex.EncodeToString(sum[:])[:16]
	if len(userPromptText) > 0 {
		if p := strings.TrimSpace(userPromptText[0]); p != "" {
			pSum := sha256.Sum256([]byte(p))
			key += "|" + hex.EncodeToString(pSum[:])[:8]
		}
	}
	return key
}

// get 命中且未过期返回 entry 与 true;否则 false(过期则主动淘汰释放槽位)。
func (c *ocrLRUCache) get(key string) (ocrCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.lru.Get(key)
	if !ok {
		return ocrCacheEntry{}, false
	}
	if time.Now().After(e.expiresAt) {
		c.lru.Remove(key)
		return ocrCacheEntry{}, false
	}
	return e, true
}

// set 写入一条缓存,根据 ok 选 TTL(成功长 TTL / 失败短 TTL 熔断窗口)。
func (c *ocrLRUCache) set(key string, text string, err error, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.ttlOk
	if !ok {
		ttl = c.ttlBad
	}
	c.lru.Add(key, ocrCacheEntry{
		text:      text,
		err:       err,
		ok:        ok,
		expiresAt: time.Now().Add(ttl),
	})
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
