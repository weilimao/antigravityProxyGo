package sigcache

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCache_StoreLoad 基本存取
func TestCache_StoreLoad(t *testing.T) {
	cache := New()
	sig := strings.Repeat("a", 60)

	cache.Store("session1", sig)
	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to find cached signature")
	}
	if loaded != sig {
		t.Errorf("Expected %q, got %q", sig, loaded)
	}
}

// TestCache_StoreRejectsShort 拒绝过短签名
func TestCache_StoreRejectsShort(t *testing.T) {
	cache := New()
	shortSig := strings.Repeat("a", 30)

	cache.Store("session1", shortSig)
	_, ok := cache.Load("session1")
	if ok {
		t.Error("Should not store signature shorter than MinSignatureLength")
	}
}

// TestCache_LongerWins 更长签名覆盖更短签名
func TestCache_LongerWins(t *testing.T) {
	cache := New()
	shortSig := strings.Repeat("a", 60)
	longSig := strings.Repeat("b", 80)

	cache.Store("session1", shortSig)
	cache.Store("session1", longSig)
	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to find cached signature")
	}
	if loaded != longSig {
		t.Errorf("Expected longer signature, got length %d", len(loaded))
	}

	// 更短签名不应覆盖更长签名
	evenShorter := strings.Repeat("c", 55)
	cache.Store("session1", evenShorter)
	loaded2, _ := cache.Load("session1")
	if loaded2 != longSig {
		t.Error("Shorter signature should not overwrite longer one")
	}
}

// TestCache_TTLExpiry TTL 过期
func TestCache_TTLExpiry(t *testing.T) {
	cache := New()
	sig := strings.Repeat("a", 60)

	// 手动插入一个已过期的条目
	cache.mu.Lock()
	cache.entries["session1"] = sigEntry{
		signature: sig,
		timestamp: time.Now().Add(-3 * time.Hour),
	}
	cache.mu.Unlock()

	_, ok := cache.Load("session1")
	if ok {
		t.Error("Expired signature should not be returned")
	}
}

// TestCache_ExtractFromSSE_Standard 从标准 Gemini SSE 格式提取
func TestCache_ExtractFromSSE_Standard(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)

	sseData := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\",\"thoughtSignature\":\"" + sig + "\"}]}}]}\n\n"
	cache.ExtractAndCacheSignatures([]byte(sseData), "session1")

	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to extract and cache signature from standard SSE")
	}
	if loaded != sig {
		t.Errorf("Expected %q, got %q", sig, loaded)
	}
}

// TestCache_ExtractFromSSE_V1Internal 从 v1internal 包装格式提取
func TestCache_ExtractFromSSE_V1Internal(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)

	sseData := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\",\"thoughtSignature\":\"" + sig + "\"}]}}]}}\n\n"
	cache.ExtractAndCacheSignatures([]byte(sseData), "session1")

	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to extract signature from v1internal wrapped SSE")
	}
	if loaded != sig {
		t.Errorf("Expected %q, got %q", sig, loaded)
	}
}

// TestCache_ExtractFromSSE_NoSignature 跳过不含签名的块
func TestCache_ExtractFromSSE_NoSignature(t *testing.T) {
	cache := New()

	sseData := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
	cache.ExtractAndCacheSignatures([]byte(sseData), "session1")

	_, ok := cache.Load("session1")
	if ok {
		t.Error("Should not cache anything when no thoughtSignature in SSE")
	}
}

// TestCache_ExtractFromFullResponse 从完整响应提取
func TestCache_ExtractFromFullResponse(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)

	respJSON := `{"response":{"candidates":[{"content":{"parts":[{"text":"hello","thoughtSignature":"` + sig + `"}]}}]}}`
	cache.ExtractFromFullResponse([]byte(respJSON), "session1")

	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to extract signature from full response")
	}
	if loaded != sig {
		t.Errorf("Expected %q, got %q", sig, loaded)
	}
}

// TestInjectCachedSignatures_ReplaceSentinel 替换哨兵为真实签名
func TestInjectCachedSignatures_ReplaceSentinel(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)
	cache.Store("session1", sig)

	req := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "model",
				"parts": []interface{}{
					map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": "shell_command",
							"args": map[string]interface{}{"command": "ls"},
						},
						"thoughtSignature": SentinelValue,
					},
				},
			},
		},
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-3.6-flash-high")

	parts := req["contents"].([]interface{})[0].(map[string]interface{})["parts"].([]interface{})
	part := parts[0].(map[string]interface{})
	injectedSig, _ := part["thoughtSignature"].(string)
	if injectedSig != sig {
		t.Errorf("Expected cached signature, got %q", injectedSig)
	}
}

// TestInjectCachedSignatures_FlashKeepSentinel Flash 模型无缓存保留哨兵
func TestInjectCachedSignatures_FlashKeepSentinel(t *testing.T) {
	cache := New()
	// 不存储任何签名

	req := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "model",
				"parts": []interface{}{
					map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": "shell_command",
						},
						"thoughtSignature": SentinelValue,
					},
				},
			},
		},
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-3.6-flash-high")

	parts := req["contents"].([]interface{})[0].(map[string]interface{})["parts"].([]interface{})
	part := parts[0].(map[string]interface{})
	sig, hasSig := part["thoughtSignature"].(string)
	if !hasSig || sig != SentinelValue {
		t.Error("Flash model should keep sentinel when no cached signature")
	}
}

// TestInjectCachedSignatures_NonFlashNoCacheKeepsSentinelInStruct 非闪存结构体路径:
// 验证非 Flash 推理模型在无缓存签名时,thoughtSignature 字段被保留为哨兵值(而非删除)。
// 旧实现 delete(partMap,"thoughtSignature") 触发上游 daily-cloudcode-pa 400
// "Function call is missing a thought_signature";本用例以结构体直接构造方式覆盖。
func TestInjectCachedSignatures_NonFlashNoCacheKeepsSentinelInStruct(t *testing.T) {
	cache := New()
	// 不存储任何签名

	req := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "model",
				"parts": []interface{}{
					map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": "shell_command",
						},
						"thoughtSignature": SentinelValue,
					},
				},
			},
		},
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-2.5-pro")

	parts := req["contents"].([]interface{})[0].(map[string]interface{})["parts"].([]interface{})
	part := parts[0].(map[string]interface{})
	sig, hasSig := part["thoughtSignature"]
	if !hasSig {
		t.Fatal("Non-Flash reasoning model should keep thoughtSignature field as sentinel when no cached signature")
	}
	if sig != SentinelValue {
		t.Errorf("thoughtSignature should remain sentinel %q, got %q", SentinelValue, sig)
	}
}

// TestInjectCachedSignatures_NonSentinelPreserved 非哨兵签名不被修改
func TestInjectCachedSignatures_NonSentinelPreserved(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)
	cache.Store("session1", sig)

	existingSig := strings.Repeat("XYZ", 30)
	req := map[string]interface{}{
		"contents": []interface{}{
			map[string]interface{}{
				"role": "model",
				"parts": []interface{}{
					map[string]interface{}{
						"functionCall": map[string]interface{}{
							"name": "shell_command",
						},
						"thoughtSignature": existingSig, // 非哨兵值
					},
				},
			},
		},
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-3.6-flash-high")

	parts := req["contents"].([]interface{})[0].(map[string]interface{})["parts"].([]interface{})
	part := parts[0].(map[string]interface{})
	currentSig, _ := part["thoughtSignature"].(string)
	if currentSig != existingSig {
		t.Error("Non-sentinel thoughtSignature should not be modified")
	}
}

// TestIsFlashModel Flash 模型检测
func TestIsFlashModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gemini-3.6-flash-high", true},
		{"gemini-2.5-flash", true},
		{"gemini-3-flash", true},
		{"gemini-2.5-pro", false},
		{"gemini-3.6-flash-high-0715", true},
		{"Gemini-3.6-Flash-High", true}, // 大小写不敏感
	}

	for _, tt := range tests {
		result := IsFlashModel(tt.model)
		if result != tt.expected {
			t.Errorf("IsFlashModel(%q) = %v, want %v", tt.model, result, tt.expected)
		}
	}
}

// TestCache_ConcurrentAccess 并发安全测试
func TestCache_ConcurrentAccess(t *testing.T) {
	cache := New()
	sig := strings.Repeat("a", 60)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Store("session1", sig+string(rune(i)))
		}(i)
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Load("session1")
		}()
	}
	wg.Wait()

	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to find cached signature after concurrent access")
	}
	if len(loaded) < MinSignatureLength {
		t.Errorf("Cached signature too short: %d", len(loaded))
	}
}

// TestCache_ExtractFromSSE_MultipleChunks 多块 SSE 提取
func TestCache_ExtractFromSSE_MultipleChunks(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)

	sseData := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hel\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\",\"thoughtSignature\":\"" + sig + "\"}]}}]}\n\n"

	cache.ExtractAndCacheSignatures([]byte(sseData), "session1")

	loaded, ok := cache.Load("session1")
	if !ok {
		t.Fatal("Expected to extract signature from multi-chunk SSE")
	}
	if loaded != sig {
		t.Errorf("Expected %q, got %q", sig, loaded)
	}
}

// TestInjectCachedSignatures_ComplexRequest 复杂请求体注入测试
func TestInjectCachedSignatures_ComplexRequest(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)
	cache.Store("session1", sig)

	inputJSON := `{
		"contents": [
			{"role": "user", "parts": [{"text": "hello"}]},
			{"role": "model", "parts": [{"functionCall": {"name": "shell_command", "args": {"command": "ls"}}, "thoughtSignature": "skip_thought_signature_validator"}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "shell_command", "response": {"output": "file1.txt"}}}]},
			{"role": "model", "parts": [{"functionCall": {"name": "read_file", "args": {"path": "file1.txt"}}, "thoughtSignature": "skip_thought_signature_validator"}]}
		]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to parse test JSON: %v", err)
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-3.6-flash-high")

	out, _ := json.Marshal(req)
	outStr := string(out)

	if strings.Contains(outStr, SentinelValue) {
		t.Error("All sentinel values should be replaced with cached signature")
	}
	if !strings.Contains(outStr, sig) {
		t.Error("Cached signature should be present in output")
	}
}

// TestInjectCachedSignatures_NonFlashNoCacheKeepsSentinel 验证非 Flash 推理模型
// 在无缓存签名时保留哨兵值,而非删除 thoughtSignature 字段。
// 这是 gemini-pro-agent 等推理模型触发上游 400
// "Function call is missing a thought_signature" 的根因回归测试。
func TestInjectCachedSignatures_NonFlashNoCacheKeepsSentinel(t *testing.T) {
	cache := New() // 空 cache,无缓存签名(hasSig=false)

	inputJSON := `{
		"contents": [
			{"role": "user", "parts": [{"text": "read this"}]},
			{"role": "model", "parts": [{"functionCall": {"name": "default_api:Read", "args": {"path": "file.txt"}}, "thoughtSignature": "skip_thought_signature_validator"}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "default_api:Read", "response": {"result": "ok"}}}]}
		]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to parse test JSON: %v", err)
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-pro-agent")

	out, _ := json.Marshal(req)
	outStr := string(out)

	if !strings.Contains(outStr, SentinelValue) {
		t.Errorf("非 Flash 推理模型无缓存签名时应保留哨兵值,但输出中找不到哨兵: %s", outStr)
	}
}

// TestInjectCachedSignatures_NonFlashWithCacheReplacesSentinel 验证非 Flash 推理模型
// 在有缓存签名时,哨兵被替换为真实签名(覆盖"有缓存"分支未回归)。
func TestInjectCachedSignatures_NonFlashWithCacheReplacesSentinel(t *testing.T) {
	cache := New()
	sig := strings.Repeat("EpAEBk", 20)
	cache.Store("session1", sig)

	inputJSON := `{
		"contents": [
			{"role": "model", "parts": [{"functionCall": {"name": "default_api:Read", "args": {"path": "file.txt"}}, "thoughtSignature": "skip_thought_signature_validator"}]}
		]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to parse test JSON: %v", err)
	}

	InjectCachedSignatures(req, cache, "session1", "gemini-pro-agent")

	out, _ := json.Marshal(req)
	outStr := string(out)

	if strings.Contains(outStr, SentinelValue) {
		t.Errorf("有缓存签名时哨兵应被替换,但输出中仍含哨兵: %s", outStr)
	}
	if !strings.Contains(outStr, sig) {
		t.Error("有缓存签名时输出应含真实缓存签名")
	}
}
