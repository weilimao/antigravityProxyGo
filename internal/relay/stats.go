package relay

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/pricing"
)

type RelayModelStats struct {
	Model        string  `json:"model"`
	RequestCount int     `json:"requestCount"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CachedTokens int     `json:"cachedTokens"`
	TotalCost    float64 `json:"totalCost"`
	LastUsedAt   string  `json:"lastUsedAt"`
}

type RelayUserStats struct {
	UserID            string                      `json:"userId"`
	UserKey           string                      `json:"userKey"`
	TotalRequests     int                         `json:"totalRequests"`
	TotalInputTokens  int                         `json:"totalInputTokens"`
	TotalOutputTokens int                         `json:"totalOutputTokens"`
	TotalCachedTokens int                         `json:"totalCachedTokens"`
	TotalCost         float64                     `json:"totalCost"`
	Models            map[string]*RelayModelStats `json:"models"`
	LastActiveAt      string                      `json:"lastActiveAt"`
	Quotas            interface{}                 `json:"quotas"`       // Optional: UserQuotas
	CurrentUsage      map[string]int64            `json:"currentUsage"` // Optional: map[string]int64
	ResetAt           map[string]string           `json:"resetAt"`      // Optional: map[string]string
}

type RelaySample struct {
	UserID       string
	UserKey      string
	ModelName    string
	InTokens     int
	OutTokens    int
	CachedTokens int
	Method       string
	Host         string
	Path         string
	SessionID    string
	DurationMs   int64
	StatusCode   int
}

type StatsTracker struct {
	sync.RWMutex
	persistPath     string
	users           map[string]*RelayUserStats
	saveTimeout     *time.Timer
	saveTimeoutLock sync.Mutex
	pricingMgr      *pricing.Manager
}

func NewStatsTracker(pricingMgr *pricing.Manager) *StatsTracker {
	return &StatsTracker{
		users:      make(map[string]*RelayUserStats),
		pricingMgr: pricingMgr,
	}
}

func (s *StatsTracker) Init(dataDir string) {
	s.Lock()
	s.persistPath = filepath.Join(dataDir, "relay_stats.json")
	s.Unlock()

	s.LoadFromDisk()
}

func (s *StatsTracker) RecordUsage(sample RelaySample) {
	if sample.ModelName == "" {
		return
	}

	s.Lock()
	defer s.Unlock()

	inTokens := sample.InTokens
	outTokens := sample.OutTokens
	cachedTokens := sample.CachedTokens
	if inTokens < 0 {
		inTokens = 0
	}
	if outTokens < 0 {
		outTokens = 0
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}

	// Get or create user stats bucket
	userBucket, exists := s.users[sample.UserID]
	if !exists {
		userBucket = &RelayUserStats{
			UserID:  sample.UserID,
			UserKey: sample.UserKey,
			Models:  make(map[string]*RelayModelStats),
		}
		s.users[sample.UserID] = userBucket
	}

	// Get or create model stats bucket
	modelBucket, exists := userBucket.Models[sample.ModelName]
	if !exists {
		modelBucket = &RelayModelStats{
			Model: sample.ModelName,
		}
		userBucket.Models[sample.ModelName] = modelBucket
	}

	// Calculate cost
	cost := s.pricingMgr.CalculateCost(sample.ModelName, inTokens, outTokens, cachedTokens)
	rate := s.pricingMgr.GetPricingForModel(sample.ModelName)
	nonCachedIn := inTokens - cachedTokens
	if nonCachedIn < 0 {
		nonCachedIn = 0
	}
	inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
	outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
	cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0
	timestamp := time.Now().Format(time.RFC3339)

	// Update model bucket
	modelBucket.RequestCount++
	modelBucket.InputTokens += inTokens
	modelBucket.OutputTokens += outTokens
	modelBucket.CachedTokens += cachedTokens
	modelBucket.TotalCost = math.Round((modelBucket.TotalCost+cost)*1000000.0) / 1000000.0
	modelBucket.LastUsedAt = timestamp

	// Update user bucket totals
	userBucket.TotalRequests++
	userBucket.TotalInputTokens += inTokens
	userBucket.TotalOutputTokens += outTokens
	userBucket.TotalCachedTokens += cachedTokens
	userBucket.TotalCost = math.Round((userBucket.TotalCost+cost)*1000000.0) / 1000000.0
	userBucket.LastActiveAt = timestamp

	// Create and insert request log into SQLite
	reqLog := &db.RequestLog{
		ReqID:        fmt.Sprintf("rl_%d", time.Now().UnixNano()),
		Timestamp:    timestamp,
		Mode:         "remote",
		UserID:       sample.UserID,
		ModelName:    sample.ModelName,
		InTokens:     inTokens,
		OutTokens:    outTokens,
		CachedTokens: cachedTokens,
		Cost:         cost,
		InputCost:    inputCost,
		OutputCost:   outputCost,
		CachedCost:   cachedCost,
		DurationMs:   sample.DurationMs,
		StatusCode:   sample.StatusCode,
		Method:       sample.Method,
		Host:         sample.Host,
		Path:         sample.Path,
		SessionID:    sample.SessionID,
	}

	go func() {
		if err := db.InsertRequestLog(reqLog); err != nil {
			fmt.Printf("[RelayStats] DB Insert Error: %v\n", err)
		}
	}()

	s.scheduleSave()
}

func (s *StatsTracker) GetUserStats(userID string) *RelayUserStats {
	s.RLock()
	defer s.RUnlock()

	bucket, exists := s.users[userID]
	if !exists {
		return nil
	}
	return deepCopyUserStats(bucket)
}

func (s *StatsTracker) GetAllUsersStats() map[string]*RelayUserStats {
	s.RLock()
	defer s.RUnlock()

	result := make(map[string]*RelayUserStats, len(s.users))
	for k, v := range s.users {
		result[k] = deepCopyUserStats(v)
	}
	return result
}

func (s *StatsTracker) scheduleSave() {
	s.saveTimeoutLock.Lock()
	defer s.saveTimeoutLock.Unlock()

	if s.saveTimeout != nil {
		return
	}

	s.saveTimeout = time.AfterFunc(3*time.Second, func() {
		s.SaveToDisk()
		s.saveTimeoutLock.Lock()
		s.saveTimeout = nil
		s.saveTimeoutLock.Unlock()
	})
}

func (s *StatsTracker) SaveToDisk() {
	s.RLock()
	path := s.persistPath
	if path == "" {
		s.RUnlock()
		return
	}

	dataCopy := make(map[string]*RelayUserStats, len(s.users))
	for k, v := range s.users {
		dataCopy[k] = deepCopyUserStats(v)
	}
	s.RUnlock()

	bytesData, err := json.MarshalIndent(dataCopy, "", "  ")
	if err != nil {
		fmt.Printf("[RelayStats] Failed to marshal stats: %v\n", err)
		return
	}

	if err := os.WriteFile(path, bytesData, 0644); err != nil {
		fmt.Printf("[RelayStats] Failed to write stats: %v\n", err)
	}
}

func (s *StatsTracker) LoadFromDisk() {
	s.Lock()
	defer s.Unlock()

	if s.persistPath == "" {
		s.users = make(map[string]*RelayUserStats)
		return
	}

	if _, err := os.Stat(s.persistPath); os.IsNotExist(err) {
		s.users = make(map[string]*RelayUserStats)
		return
	}

	raw, err := os.ReadFile(s.persistPath)
	if err != nil {
		s.users = make(map[string]*RelayUserStats)
		return
	}

	var loaded map[string]*RelayUserStats
	if err := json.Unmarshal(raw, &loaded); err != nil {
		s.users = make(map[string]*RelayUserStats)
		return
	}

	// Ensure all model maps are initialized
	for _, u := range loaded {
		if u.Models == nil {
			u.Models = make(map[string]*RelayModelStats)
		}
	}
	s.users = loaded
}

func (s *StatsTracker) UpdatePath(newDir string) {
	s.Lock()
	if s.saveTimeout != nil {
		s.saveTimeout.Stop()
		s.saveTimeout = nil
	}
	s.Unlock()

	s.SaveToDisk()

	s.Lock()
	s.persistPath = filepath.Join(newDir, "relay_stats.json")
	s.Unlock()

	s.LoadFromDisk()
}

func deepCopyUserStats(src *RelayUserStats) *RelayUserStats {
	copied := *src
	copied.Models = make(map[string]*RelayModelStats, len(src.Models))
	for k, v := range src.Models {
		ms := *v
		copied.Models[k] = &ms
	}
	return &copied
}
