package proxy

import "antigravity-proxy/internal/sigcache"

// 以下函数为对 internal/sigcache 包的薄包装，
// 保留 proxy 包内原有的调用点签名（GetSignatureCache / InjectCachedSignatures），
// 避免改动 handler.go 的调用方。

// GetSignatureCache 返回全局签名缓存单例
func GetSignatureCache() *sigcache.Cache {
	return sigcache.GetGlobal()
}

// InjectCachedSignatures 将缓存的 thoughtSignature 注入到请求体的 functionCall parts。
// proxy 包内的 3 参数包装：自动使用全局单例缓存。
func InjectCachedSignatures(req map[string]interface{}, sessionKey string, modelName string) {
	sigcache.InjectCachedSignatures(req, sigcache.GetGlobal(), sessionKey, modelName)
}
