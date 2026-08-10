package pricing

import (
	"testing"
)

func newTestManager() *Manager {
	m := NewManager()
	m.initialized = false
	m.copyDefaults()
	m.initialized = true
	return m
}

// TestGetPricingForModel_SlashBaseFallback 覆盖三阶降级链路:
//  1. 精确全名命中:        "gpt-oss-120b-medium" -> exactMappings -> gpt-oss 120b (medium)
//  2. 全名不含映射但被模糊/包含规则兜住: "foo/gemini-3.5-flash" 截断到 gemini-3.5-flash 后按包含匹配 medium
//  3. 斜杠后缀逐级截断:   "z-ai/gpt-oss-120b" 全名未命中, 截断到 gpt-oss-120b 后在 currentPricing 中直接命中
//  4. 前缀仅能截断一次:   "x/gpt-oss-120b" 截一次即命中, 剩余不存在; 循环收敛不越界
//  5. 极端兜底:           "unknown" 配置自身被命中 -> unknown 档
//  6. 空模型兜底:         "" -> unknown 档
//  7. 分级嵌套:           "p1/z-ai/glm-5.2" 逐步截断直至基名最终失败 -> unknown
//  8. 未配置带前缀模型上配置键, 全名优先于基名: 全名 "nest/gemini-3.1-pro" 存在时命中全名档
func TestGetPricingForModel_SlashBaseFallback(t *testing.T) {
	m := newTestManager()

	// 1. exactMappings 直查
	if got := m.GetPricingForModel("gpt-oss-120b-medium"); got.Input != defaultPricing["gpt-oss 120b (medium)"].Input {
		t.Errorf("exact mapping: got %v want %v", got, defaultPricing["gpt-oss 120b (medium)"])
	}

	// 2. 斜杠前缀 + 包含匹配兜底
	mediumFlash := m.GetPricingForModel("foo/gemini-3.5-flash")
	if mediumFlash.Input != defaultPricing["gemini 3.5 flash (medium)"].Input {
		t.Errorf("prefix+contains: got %v want medium flash", mediumFlash)
	}

	// 3. 斜杠后缀基名直接命中 currentPricing 键
	base := m.GetPricingForModel("z-ai/gpt-oss-120b")
	if base.Input != defaultPricing["gpt-oss 120b (medium)"].Input {
		t.Errorf("slash base hit: got %v want gpt-oss 120b", base)
	}

	// 4. 单级前缀截断一次即命中, 循环收敛不越界
	shallow := m.GetPricingForModel("x/gpt-oss-120b")
	if shallow.Input != base.Input {
		t.Errorf("single prefix: got %v want %v", shallow, base)
	}

	// 5. unknown 配置键自身可被命中
	if got := m.GetPricingForModel("utils/unknown"); got.Input != defaultPricing["unknown"].Input {
		t.Errorf("unknown key: got %v", got)
	}

	// 6. 空模型兜底
	if got := m.GetPricingForModel(""); got.Input != defaultPricing["unknown"].Input {
		t.Errorf("empty model: got %v", got)
	}

	// 7. 三层前缀逐级截断直至基名失败 -> unknown
	if got := m.GetPricingForModel("p1/z-ai/glm-5.2"); got.Input != defaultPricing["unknown"].Input {
		t.Errorf("deep prefix fallback: got %v want unknown", got)
	}

	// 8. 全名优先于基名
	m.currentPricing["nest/gemini-3.1-pro"] = ModelRate{Input: 42.0, Output: 100.0, Cached: 10.0}
	defer delete(m.currentPricing, "nest/gemini-3.1-pro")
	if got := m.GetPricingForModel("nest/gemini-3.1-pro"); got.Input != 42.0 {
		t.Errorf("full name preferred: got %v want 42", got)
	}
	// 去掉前缀后应回退到基名规则(low档), 证明全名命中是显式的而非截断结果
	if got := m.GetPricingForModel("gemini-3.1-pro"); got.Input == 42.0 || got.Input != defaultPricing["gemini 3.1 pro (low)"].Input {
		t.Errorf("base name not polluted: got %v", got)
	}
}