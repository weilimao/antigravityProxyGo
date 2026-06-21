package pricing

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ModelRate struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cached float64 `json:"cached"`
}

type CostBreakdown struct {
	InputCost   float64 `json:"inputCost"`
	OutputCost  float64 `json:"outputCost"`
	CachedCost  float64 `json:"cachedCost"`
	TotalCost   float64 `json:"totalCost"`
	NonCachedIn int     `json:"nonCachedIn"`
}

var defaultPricing = map[string]ModelRate{
	"gemini 3.5 flash (medium)":   {Input: 1.50, Output: 9.00, Cached: 0.375},
	"gemini 3.5 flash (high)":     {Input: 1.50, Output: 9.00, Cached: 0.375},
	"gemini 3.5 flash (low)":      {Input: 1.50, Output: 9.00, Cached: 0.375},
	"gemini 3.1 pro (low)":        {Input: 2.00, Output: 12.00, Cached: 0.50},
	"gemini 3.1 pro (high)":       {Input: 2.00, Output: 12.00, Cached: 0.50},
	"claude sonnet 4.6 (thinking)": {Input: 3.00, Output: 15.00, Cached: 0.75},
	"claude opus 4.6 (thinking)":   {Input: 5.00, Output: 25.00, Cached: 1.25},
	"gpt-oss 120b (medium)":       {Input: 0.15, Output: 0.60, Cached: 0.0375},
	"unknown":                     {Input: 1.00, Output: 3.00, Cached: 0.25},
}

type Manager struct {
	sync.RWMutex
	customUserDataPath string
	pricingFilePath    string
	currentPricing     map[string]ModelRate
	initialized        bool
}

func NewManager() *Manager {
	return &Manager{
		currentPricing: make(map[string]ModelRate),
	}
}

func (m *Manager) Init(userDataPath string) {
	m.Lock()
	m.customUserDataPath = userDataPath
	m.pricingFilePath = filepath.Join(userDataPath, "pricing.json")
	m.initialized = false
	m.Unlock()

	m.EnsureInitialized()
}

func (m *Manager) UpdatePath(newPath string) {
	m.Init(newPath)
}

func (m *Manager) EnsureInitialized() {
	m.Lock()
	defer m.Unlock()

	if m.initialized {
		return
	}

	m.loadPricing()
	m.initialized = true
}

func (m *Manager) loadPricing() {
	if _, err := os.Stat(m.pricingFilePath); os.IsNotExist(err) {
		m.copyDefaults()
		return
	}

	data, err := os.ReadFile(m.pricingFilePath)
	if err != nil {
		m.copyDefaults()
		return
	}

	var parsed map[string]ModelRate
	if err := json.Unmarshal(data, &parsed); err != nil {
		m.copyDefaults()
		return
	}

	// 自动检查迁移旧字段
	if _, existsOld := parsed["deepseek-v3"]; existsOld || parsed["gemini 3.5 flash (medium)"].Input == 0 {
		m.copyDefaults()
		_ = m.savePricing()
	} else {
		m.currentPricing = make(map[string]ModelRate)
		for k, v := range defaultPricing {
			m.currentPricing[k] = v
		}
		for k, v := range parsed {
			m.currentPricing[k] = v
		}
	}
}

func (m *Manager) copyDefaults() {
	m.currentPricing = make(map[string]ModelRate)
	for k, v := range defaultPricing {
		m.currentPricing[k] = v
	}
}

func (m *Manager) savePricing() error {
	data, err := json.MarshalIndent(m.currentPricing, "", "    ")
	if err != nil {
		return err
	}

	err = os.WriteFile(m.pricingFilePath, data, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) GetAllPricing() map[string]ModelRate {
	m.EnsureInitialized()
	m.RLock()
	defer m.RUnlock()

	res := make(map[string]ModelRate)
	for k, v := range m.currentPricing {
		res[k] = v
	}
	return res
}

func (m *Manager) GetPricingForModel(modelName string) ModelRate {
	m.EnsureInitialized()
	m.RLock()
	defer m.RUnlock()

	if modelName == "" {
		return m.currentPricing["unknown"]
	}

	name := strings.TrimSpace(strings.ToLower(modelName))

	// 精确匹配字典
	exactMappings := map[string]string{
		"gemini-3-flash-agent":     "gemini 3.5 flash (high)",
		"gemini-3.5-flash-low":     "gemini 3.5 flash (medium)",
		"gemini-3.5-flash-extra-low": "gemini 3.5 flash (low)",
		"gemini-pro-agent":         "gemini 3.1 pro (high)",
		"gemini-3.1-pro-low":       "gemini 3.1 pro (low)",
		"claude-sonnet-4-6":        "claude sonnet 4.6 (thinking)",
		"claude-opus-4-6-thinking": "claude opus 4.6 (thinking)",
		"gpt-oss-120b-medium":      "gpt-oss 120b (medium)",
	}

	if targetKey, found := exactMappings[name]; found {
		if rate, ok := m.currentPricing[targetKey]; ok {
			return rate
		}
	}

	// 直接键匹配
	if rate, ok := m.currentPricing[name]; ok {
		return rate
	}

	// 模糊匹配逻辑
	if strings.Contains(name, "gemini 3.5 flash") || strings.Contains(name, "gemini-3.5-flash") {
		if strings.Contains(name, "high") {
			return m.currentPricing["gemini 3.5 flash (high)"]
		}
		if strings.Contains(name, "low") {
			return m.currentPricing["gemini 3.5 flash (low)"]
		}
		return m.currentPricing["gemini 3.5 flash (medium)"]
	}
	if strings.Contains(name, "gemini 3.1 pro") || strings.Contains(name, "gemini-3.1-pro") {
		if strings.Contains(name, "high") {
			return m.currentPricing["gemini 3.1 pro (high)"]
		}
		return m.currentPricing["gemini 3.1 pro (low)"]
	}
	if strings.Contains(name, "claude sonnet 4.6") || strings.Contains(name, "sonnet 4.6") || (strings.Contains(name, "sonnet") && strings.Contains(name, "thinking")) {
		return m.currentPricing["claude sonnet 4.6 (thinking)"]
	}
	if strings.Contains(name, "claude opus 4.6") || strings.Contains(name, "opus 4.6") || (strings.Contains(name, "opus") && strings.Contains(name, "thinking")) {
		return m.currentPricing["claude opus 4.6 (thinking)"]
	}
	if strings.Contains(name, "gpt-oss 120b") || strings.Contains(name, "gpt-oss-120b") || strings.Contains(name, "oss 120b") || strings.Contains(name, "oss-120b") {
		return m.currentPricing["gpt-oss 120b (medium)"]
	}

	// 家族模糊备选
	if strings.Contains(name, "flash") {
		return m.currentPricing["gemini 3.5 flash (medium)"]
	}
	if strings.Contains(name, "pro") {
		return m.currentPricing["gemini 3.1 pro (low)"]
	}
	if strings.Contains(name, "sonnet") {
		return m.currentPricing["claude sonnet 4.6 (thinking)"]
	}
	if strings.Contains(name, "opus") {
		return m.currentPricing["claude opus 4.6 (thinking)"]
	}

	return m.currentPricing["unknown"]
}

func (m *Manager) CalculateCostBreakdown(modelName string, inTokens, outTokens, cachedTokens int) CostBreakdown {
	rate := m.GetPricingForModel(modelName)
	nonCachedIn := inTokens - cachedTokens
	if nonCachedIn < 0 {
		nonCachedIn = 0
	}

	inputCost := float64(nonCachedIn) * rate.Input / 1000000.0
	outputCost := float64(outTokens) * rate.Output / 1000000.0
	cachedCost := float64(cachedTokens) * rate.Cached / 1000000.0
	
	// 精度微调
	inputCost = math.Round(inputCost*1000000.0) / 1000000.0
	outputCost = math.Round(outputCost*1000000.0) / 1000000.0
	cachedCost = math.Round(cachedCost*1000000.0) / 1000000.0
	totalCost := math.Round((inputCost+outputCost+cachedCost)*1000000.0) / 1000000.0

	return CostBreakdown{
		InputCost:   inputCost,
		OutputCost:  outputCost,
		CachedCost:  cachedCost,
		TotalCost:   totalCost,
		NonCachedIn: nonCachedIn,
	}
}

func (m *Manager) CalculateCost(modelName string, inTokens, outTokens, cachedTokens int) float64 {
	return m.CalculateCostBreakdown(modelName, inTokens, outTokens, cachedTokens).TotalCost
}

func (m *Manager) UpdateModelPricing(modelKey string, rate ModelRate) error {
	m.EnsureInitialized()
	m.Lock()
	m.currentPricing[strings.ToLower(modelKey)] = rate
	m.Unlock()
	return m.savePricing()
}

func (m *Manager) DeleteModelPricing(modelKey string) bool {
	m.EnsureInitialized()
	key := strings.ToLower(modelKey)
	if key == "unknown" {
		return false
	}

	m.Lock()
	defer m.Unlock()
	if _, ok := m.currentPricing[key]; ok {
		delete(m.currentPricing, key)
		_ = m.savePricing()
		return true
	}
	return false
}

func (m *Manager) ResetPricingToDefault() error {
	m.Lock()
	m.copyDefaults()
	_ = os.Remove(m.pricingFilePath)
	m.Unlock()
	return nil
}
