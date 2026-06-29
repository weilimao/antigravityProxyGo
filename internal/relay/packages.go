package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type RelayPackageTemplate struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Quotas UserQuotas `json:"quotas"`
}

type PackageManager struct {
	sync.RWMutex
	packages    []*RelayPackageTemplate
	persistPath string
}

func NewPackageManager() *PackageManager {
	return &PackageManager{
		packages: make([]*RelayPackageTemplate, 0),
	}
}

func (m *PackageManager) Init(dataDir string) {
	m.Lock()
	m.persistPath = filepath.Join(dataDir, "relay_packages.json")
	m.Unlock()
	m.LoadFromDisk()
	
	// Seed default packages if empty
	if len(m.GetPackages()) == 0 {
		m.AddPackage("Pro", UserQuotas{
			Gemini: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 50000, EnableDaily: true, DailyDays: 7, DailyTokens: 500000},
			Claude: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 50000, EnableDaily: true, DailyDays: 7, DailyTokens: 500000},
			ValidDuration: 1, ValidUnit: "months",
			RateLimit: 30,
		})
		m.AddPackage("Pro 5x", UserQuotas{
			Gemini: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 250000, EnableDaily: true, DailyDays: 7, DailyTokens: 2500000},
			Claude: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 250000, EnableDaily: true, DailyDays: 7, DailyTokens: 2500000},
			ValidDuration: 1, ValidUnit: "months",
			RateLimit: 30,
		})
		m.AddPackage("Pro 20x", UserQuotas{
			Gemini: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 1000000, EnableDaily: true, DailyDays: 7, DailyTokens: 10000000},
			Claude: ModelQuota{EnableHourly: true, HourlyHours: 5, HourlyTokens: 1000000, EnableDaily: true, DailyDays: 7, DailyTokens: 10000000},
			ValidDuration: 1, ValidUnit: "months",
			RateLimit: 30,
		})
	}
}

func (m *PackageManager) AddPackage(name string, quotas UserQuotas) (*RelayPackageTemplate, error) {
	m.Lock()
	defer m.Unlock()

	b := make([]byte, 4)
	rand.Read(b)
	id := hex.EncodeToString(b)

	pkg := &RelayPackageTemplate{
		ID:     id,
		Name:   name,
		Quotas: quotas,
	}

	m.packages = append(m.packages, pkg)
	m.saveToDiskLocked()
	return pkg, nil
}

func (m *PackageManager) UpdatePackage(id string, name string, quotas UserQuotas) error {
	m.Lock()
	defer m.Unlock()

	for _, p := range m.packages {
		if p.ID == id {
			p.Name = name
			p.Quotas = quotas
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("package not found")
}

func (m *PackageManager) DeletePackage(id string) error {
	m.Lock()
	defer m.Unlock()

	for i, p := range m.packages {
		if p.ID == id {
			m.packages = append(m.packages[:i], m.packages[i+1:]...)
			m.saveToDiskLocked()
			return nil
		}
	}
	return fmt.Errorf("package not found")
}

func (m *PackageManager) GetPackages() []*RelayPackageTemplate {
	m.RLock()
	defer m.RUnlock()

	out := make([]*RelayPackageTemplate, len(m.packages))
	for i, p := range m.packages {
		pc := *p
		out[i] = &pc
	}
	return out
}

func (m *PackageManager) saveToDiskLocked() {
	if m.persistPath == "" {
		return
	}
	data, err := json.MarshalIndent(m.packages, "", "  ")
	if err == nil {
		os.WriteFile(m.persistPath, data, 0644)
	}
}

func (m *PackageManager) LoadFromDisk() {
	m.Lock()
	defer m.Unlock()

	if m.persistPath == "" {
		return
	}
	data, err := os.ReadFile(m.persistPath)
	if err == nil {
		var pkgs []*RelayPackageTemplate
		if err := json.Unmarshal(data, &pkgs); err == nil {
			m.packages = pkgs
		}
	}
}
