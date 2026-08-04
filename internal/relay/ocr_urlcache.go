package relay

// ocr_urlcache.go —— URL→base64 的二级缓存(配合 ocr_cache.go 的 b64→text 一级缓存)。
//
// 三层分离架构的 L1 缓存补充:URL 类型图片下载后,base64 体积可达数 MB,若每轮 Claude Code
// 重发都重新下载(~5s + 出站带宽)极浪费。本缓存以 url 的 sha256 为键,缓存"下载所得 base64 + mime",
// 使相同 URL 图在 24h 内免下载;再由 ocr_cache(b64→text)使相同图免 OCR。两级解耦:
//   - urlCache 命中 → 跳过下载,直接拿 b64 进 OCR 链路;
//   - ocrCache 命中 → 跳过 OCR,直接拿文本。
// 反之 OCR 模型切换时 b64→text 失效但 url→b64 仍命中,只重 OCR 不重下载。
//
// 内存保护:base64 单条上限 ~13MB(对应 10MB 原图),故容量取 64 且超 5MB 的 b64 不入本缓存
// (避免长跑进程被大图集撑爆内存,与 memory 记录的收紧策略一致)。LRU 按最近访问淘汰。
// 进程内易失,重启重新下载一次可接受。

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// urlB64CacheCap 是 urlCache 的 LRU 容量上限,兼顾命中率与内存(见文件头注释)。
const urlB64CacheCap = 64

// urlB64CacheMaxStoreBytes 是单条 b64 超过此字节数则不入 urlCache(防内存放大)。
const urlB64CacheMaxStoreBytes = 5 << 20

// urlB64CacheTTL 是 url→b64 缓存的成功 TTL,与 ocrCache 成功 TTL 对齐(24h)。
const urlB64CacheTTL = 24 * time.Hour

// urlB64Entry 是单条 URL→base64 缓存条目。
type urlB64Entry struct {
	b64       string
	mime      string
	expiresAt time.Time
}

// urlB64LRUCache 是 URL→base64 的进程内 LRU + TTL 缓存。
type urlB64LRUCache struct {
	mu  sync.Mutex
	lru *lru.Cache[string, urlB64Entry]
}

// newUrlB64LRUCache 构造一个 url→b64 缓存。capacity<=0 用默认 urlB64CacheCap。
func newUrlB64LRUCache(capacity int) *urlB64LRUCache {
	if capacity <= 0 {
		capacity = urlB64CacheCap
	}
	c, _ := lru.New[string, urlB64Entry](capacity)
	return &urlB64LRUCache{lru: c}
}

// urlCacheKey 返回 url 的 sha256 前 16 字节 hex(定长 32 字符)键。
// url 可能数 KB,不直接做键;同 url 同键,跨会话共享(下载结果是URL决定的全局资源,与会话无关)。
func urlCacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:16]
}

// get 命中且未过期返回 (b64, mime, true);否则 ("", "", false)。
func (c *urlB64LRUCache) get(key string) (string, string, bool) {
	if c == nil {
		return "", "", false
	}
	c.mu.Lock()
	e, ok := c.lru.Get(key)
	c.mu.Unlock()
	if !ok || time.Now().After(e.expiresAt) {
		if ok {
			c.mu.Lock()
			c.lru.Remove(key)
			c.mu.Unlock()
		}
		return "", "", false
	}
	return e.b64, e.mime, true
}

// set 写入一条 url→b64 缓存。b64 超 urlB64CacheMaxStoreBytes 则跳过(防内存放大)。
// 调用方应传入"成功下载并校验过"的 b64/mime。
func (c *urlB64LRUCache) set(key, b64, mime string) {
	if c == nil {
		return
	}
	if len(b64) > urlB64CacheMaxStoreBytes {
		return // 超大图不缓存,优先保内存
	}
	c.mu.Lock()
	c.lru.Add(key, urlB64Entry{
		b64:       b64,
		mime:      mime,
		expiresAt: time.Now().Add(urlB64CacheTTL),
	})
	c.mu.Unlock()
}
