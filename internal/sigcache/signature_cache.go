// Package sigcache 提供 thoughtSignature 的会话级缓存能力。
//
// 从 Google SSE 响应中提取 thoughtSignature 并缓存，下次请求注入到
// functionCall parts，保证 v1internal API 的思考链连续性，
// 防止模型丢失上下文后重复生成失败的工具调用。
//
// 参考 Antigravity-Manager 的 SignatureCache 实现。
package sigcache

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const (
	SignatureTTL       = 2 * time.Hour
	MinSignatureLength = 50
	MaxCacheEntries    = 500
	cleanupInterval    = 5 * time.Minute
)

// SentinelValue 是 Flash 模型无缓存签名时使用的哨兵值，
// 翻译层在 functionCall part 上注入此值作为标记，由本包替换为真实签名。
const SentinelValue = "skip_thought_signature_validator"

// sigEntry 缓存条目：保存签名值和写入时间
type sigEntry struct {
	signature string
	timestamp time.Time
}

// Cache 基于 session 的 thoughtSignature 缓存。
type Cache struct {
	mu      sync.RWMutex
	entries map[string]sigEntry // sessionKey -> latest signature
}

var globalCache *Cache
var cacheOnce sync.Once

// GetGlobal 返回全局单例签名缓存
func GetGlobal() *Cache {
	cacheOnce.Do(func() {
		globalCache = &Cache{
			entries: make(map[string]sigEntry),
		}
		go globalCache.cleanupLoop()
	})
	return globalCache
}

// StopGlobal 供应用优雅关闭时使用
func StopGlobal() {
	// SigCache 无长连接无损写，为防泄漏预留
}

// New 创建独立实例（主要用于测试）
func New() *Cache {
	return &Cache{entries: make(map[string]sigEntry)}
}

// Store 保存签名到缓存。仅接受长度 >= MinSignatureLength 的签名，
// 且仅当新签名比已有签名更长时才覆盖（防止部分签名覆盖完整签名）。
func (c *Cache) Store(sessionKey string, signature string) {
	if len(signature) < MinSignatureLength {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	existing, found := c.entries[sessionKey]
	if found && len(existing.signature) >= len(signature) {
		return // 不覆盖更长的已有签名
	}
	c.entries[sessionKey] = sigEntry{
		signature: signature,
		timestamp: time.Now(),
	}

	// 超限时驱逐过期条目
	if len(c.entries) > MaxCacheEntries {
		now := time.Now()
		for k, v := range c.entries {
			if now.Sub(v.timestamp) > SignatureTTL {
				delete(c.entries, k)
			}
		}
	}
}

// Load 读取缓存签名。返回签名值和是否找到有效条目（未过期）。
func (c *Cache) Load(sessionKey string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, found := c.entries[sessionKey]
	if !found || time.Since(entry.timestamp) > SignatureTTL {
		return "", false
	}
	return entry.signature, true
}

// cleanupLoop 后台定期清理过期缓存条目
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.Sub(v.timestamp) > SignatureTTL {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}

// ExtractAndCacheSignatures 从 SSE 流块中提取 thoughtSignature 并缓存。
// 这是最佳努力提取：跳过不含 thoughtSignature 的块，容忍跨块的部分 JSON。
func (c *Cache) ExtractAndCacheSignatures(data []byte, sessionKey string) {
	// 快速路径：跳过不含 thoughtSignature 的块
	if !bytes.Contains(data, []byte("thoughtSignature")) {
		return
	}

	// 逐行扫描 SSE data: 行
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		jsonData := bytes.TrimPrefix(line, []byte("data:"))
		jsonData = bytes.TrimSpace(jsonData)
		if len(jsonData) == 0 {
			continue
		}

		var doc map[string]interface{}
		if err := json.Unmarshal(jsonData, &doc); err != nil {
			continue // 解析失败，跳过
		}

		// 处理 v1internal 包装：{"response": {...}}
		actualData := doc
		if inner, ok := doc["response"].(map[string]interface{}); ok {
			actualData = inner
		}

		c.extractFromCandidates(actualData, sessionKey)
	}
}

// ExtractFromFullResponse 从完整（非流式）响应中提取签名。
func (c *Cache) ExtractFromFullResponse(data []byte, sessionKey string) {
	if !bytes.Contains(data, []byte("thoughtSignature")) {
		return
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return
	}

	// 处理 v1internal 包装
	actualData := doc
	if inner, ok := doc["response"].(map[string]interface{}); ok {
		actualData = inner
	}

	c.extractFromCandidates(actualData, sessionKey)
}

// extractFromCandidates 从 candidates 数组中提取 thoughtSignature
func (c *Cache) extractFromCandidates(data map[string]interface{}, sessionKey string) {
	candidates, ok := data["candidates"].([]interface{})
	if !ok {
		return
	}
	for _, cand := range candidates {
		candMap, ok := cand.(map[string]interface{})
		if !ok {
			continue
		}
		contentMap, ok := candMap["content"].(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := contentMap["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, part := range parts {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if sig, ok := partMap["thoughtSignature"].(string); ok && len(sig) >= MinSignatureLength {
				c.Store(sessionKey, sig)
			}
		}
	}
}

// InjectCachedSignatures 遍历请求体的 contents[].parts[]，
// 将 functionCall part 中的哨兵值替换为缓存的真实签名。
// 使用传入的 cache 实例。
//
// 规则：
//   - 有缓存签名 -> 替换哨兵为真实签名
//   - 无缓存签名 -> 统一保留哨兵值（v1internal 上游要求 functionCall parts 必带 thoughtSignature）
//
// 注意：IsFlashModel 仍由本包导出供其他模块复用，但本注入函数不再按 isFlash 分流
// 删除字段——旧逻辑对非 Flash 模型 delete thoughtSignature 会触发上游 400
// "Function call is missing a thought_signature"。哨兵值已是上游识别的跳过校验标记。
func InjectCachedSignatures(req map[string]interface{}, cache *Cache, sessionKey string, modelName string) {
	cachedSig, hasSig := cache.Load(sessionKey)

	// 遍历 request.contents[].parts[]
	contents, ok := req["contents"].([]interface{})
	if !ok {
		return
	}
	for _, contentItem := range contents {
		contentMap, ok := contentItem.(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := contentMap["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, partItem := range parts {
			partMap, ok := partItem.(map[string]interface{})
			if !ok {
				continue
			}
			// 只处理包含 functionCall 的 part
			if _, hasFC := partMap["functionCall"]; hasFC {
				currentSig, _ := partMap["thoughtSignature"].(string)
				if currentSig == SentinelValue {
					if hasSig {
						partMap["thoughtSignature"] = cachedSig
					} else {
						// 无缓存签名时统一保留哨兵值,不再按 isFlash 分流删除字段。
						// v1internal 上游(daily-cloudcode-pa)对推理模型(flash/pro/agent/thinking 等)
						// 强制要求 functionCall parts 携带 thoughtSignature 字段,缺失即返回
						// 400 "Function call is missing a thought_signature in functionCall parts"。
						// 哨兵值 SentinelValue("skip_thought_signature_validator")是上游识别的
						// "跳过签名校验"标记,Flash 路径已长期验证其被接受,故对非 Flash 推理模型
						// (如 gemini-pro-agent)同样保留哨兵即可。
						// 旧逻辑对非 Flash 模型走 delete(partMap,"thoughtSignature") 会删掉字段,
						// 导致 gemini-pro-agent 等推理模型在无缓存签名时触发上游 400。
						// 仅在纯降级路径(标准 generativelanguage)由 cleanAndPrepareGeminiRequest→
						// stripThoughtSignature 独立全删 thoughtSignature,与本函数职责分离,零回归。
						partMap["thoughtSignature"] = SentinelValue
					}
				}
			}
		}
	}
}

// IsFlashModel 检测是否为 Flash 系列模型
func IsFlashModel(modelName string) bool {
	m := strings.ToLower(modelName)
	return strings.Contains(m, "flash")
}
