package service

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-web-platform/internal/model"
)

// AccountService 负责账号池管理及各通道账号的加载、维护与持久化
type AccountService struct {
	customDir string
	mu        sync.RWMutex
}

func NewAccountService() *AccountService {
	return &AccountService{}
}

// NewAccountServiceWithDir 供单元测试注入隔离沙箱目录
func NewAccountServiceWithDir(dir string) *AccountService {
	return &AccountService{customDir: dir}
}

// partitionFileName 根据 provider 映射对应磁盘分区文件名
func (s *AccountService) partitionFileName(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "nvidia":
		return "accounts_nvidia.json"
	case "other":
		return "accounts_other.json"
	case "grok":
		return "accounts_grok.json"
	case "workbuddy":
		return "accounts_workbuddy.json"
	case "project":
		return "accounts_project.json"
	case "antigravity":
		return "accounts_antigravity.json"
	default:
		return "accounts_" + p + ".json"
	}
}

// getTargetFilePath 获取分区文件的实际读写绝对路径
func (s *AccountService) getTargetFilePath(filename string) string {
	if s.customDir != "" {
		return filepath.Join(s.customDir, filename)
	}

	// 优先沿用已存在的系统分区文件
	if existing := findLocalAccountsFile(filename); existing != "" {
		return existing
	}

	// 查找任意其它已存在的 accounts 分区，使用其同级目录
	for _, candidate := range []string{"accounts_pool.json", "accounts_nvidia.json", "accounts_other.json"} {
		if ex := findLocalAccountsFile(candidate); ex != "" {
			return filepath.Join(filepath.Dir(ex), filename)
		}
	}

	// 兜底写入 AppData remote_sandbox 目录
	if appData := os.Getenv("APPDATA"); appData != "" {
		dir := filepath.Join(appData, "antigravity-proxy-desktop", "remote_sandbox")
		_ = os.MkdirAll(dir, 0755)
		return filepath.Join(dir, filename)
	}

	return filename
}

type accountsShell struct {
	Accounts []*model.AccountDTO `json:"accounts"`
}

// readPartitionAccounts 读取单个分区文件中的账号列表
func (s *AccountService) readPartitionAccounts(filename string) ([]*model.AccountDTO, string) {
	path := s.getTargetFilePath(filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path
	}

	var shell accountsShell
	if err := json.Unmarshal(data, &shell); err != nil {
		return nil, path
	}

	return shell.Accounts, path
}

// writePartitionAccounts 安全持久化写入单个分区文件
func (s *AccountService) writePartitionAccounts(filename string, accounts []*model.AccountDTO) error {
	path := s.getTargetFilePath(filename)
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	shell := accountsShell{Accounts: accounts}
	data, err := json.MarshalIndent(shell, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化账号分区数据失败: %w", err)
	}

	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		// Windows 覆盖时可能需要先删除目标
		_ = os.Remove(path)
		if renameErr := os.Rename(tmpFile, path); renameErr != nil {
			return fmt.Errorf("重命名替换分区文件失败: %w", renameErr)
		}
	}

	return nil
}

// maskKey 辅助生成脱敏 API Key 展示串
func maskKey(k string) string {
	trimmed := strings.TrimSpace(k)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 8 {
		return "****"
	}
	return trimmed[:4] + "****" + trimmed[len(trimmed)-4:]
}

// GetAccountsData 获取所有通道账号及全局号池配置
func (s *AccountService) GetAccountsData() (*model.AccountsDataResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. 读取 accounts_pool.json
	cfg := s.loadPoolConfigInternal()

	// 2. 读取各分区账号
	var allAccounts []*model.AccountDTO
	partitions := []string{"antigravity", "project", "nvidia", "grok", "workbuddy", "other"}
	for _, p := range partitions {
		fn := s.partitionFileName(p)
		accs, _ := s.readPartitionAccounts(fn)
		for _, a := range accs {
			if a.Provider == "" {
				a.Provider = p
			}
			a.MaskedKey = maskKey(a.AccessToken)
			// 保留明文 token 在 DTO 中以备内部操作，返回前端时视安全策略使用
			allAccounts = append(allAccounts, a)
		}
	}

	// 3. 统计 Other 号池的二级子渠道信息
	otherGroups := s.extractOtherGroups(allAccounts)

	return &model.AccountsDataResponse{
		Accounts: allAccounts,
		Config:   cfg,
		Groups:   otherGroups,
	}, nil
}

func (s *AccountService) loadPoolConfigInternal() *model.PoolConfigDTO {
	defaultCfg := &model.PoolConfigDTO{
		PoolMode:                  true,
		ActiveChannel:             "nvidia",
		OtherLBModes:              make(map[string]string),
		NvidiaLBMode:              "round-robin",
		GrokLBMode:                "round-robin",
		WorkbuddyLBMode:           "round-robin",
		NvidiaMaxConcurrency:      40,
		AntigravityMaxConcurrency: 10,
		AntigravityCliVersion:     "2.3.1",
		ProjectMaxConcurrency:     10,
		OtherMaxConcurrency:       make(map[string]int),
		OtherWorkerProxyURLs:      make(map[string]string),
		OtherWorkerProxyEnabled:   make(map[string]bool),
		GrokMaxConcurrency:        10,
		GrokCliVersion:            "1.0.0",
		GrokQuotaCooldownHours:    24,
		WorkbuddyMaxConcurrency:   10,
	}

	path := s.getTargetFilePath("accounts_pool.json")
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, defaultCfg)
	}

	if defaultCfg.OtherLBModes == nil {
		defaultCfg.OtherLBModes = make(map[string]string)
	}
	if defaultCfg.OtherMaxConcurrency == nil {
		defaultCfg.OtherMaxConcurrency = make(map[string]int)
	}
	if defaultCfg.NvidiaLBMode == "" {
		defaultCfg.NvidiaLBMode = "round-robin"
	}
	if defaultCfg.NvidiaMaxConcurrency <= 0 {
		defaultCfg.NvidiaMaxConcurrency = 40
	}

	return defaultCfg
}

func (s *AccountService) extractOtherGroups(accounts []*model.AccountDTO) []model.OtherGroupInfo {
	groupMap := make(map[string]*model.OtherGroupInfo)
	for _, a := range accounts {
		if strings.ToLower(a.Provider) != "other" || a.GroupID == "" {
			continue
		}
		info, exists := groupMap[a.GroupID]
		if !exists {
			name := a.GroupName
			if name == "" {
				name = a.GroupID
			}
			info = &model.OtherGroupInfo{
				GroupID:      a.GroupID,
				GroupName:    name,
				Formats:      a.Formats,
				AccountCount: 0,
				EnabledCount: 0,
			}
			groupMap[a.GroupID] = info
		}
		info.AccountCount++
		if a.Enabled {
			info.EnabledCount++
		}
	}

	res := make([]model.OtherGroupInfo, 0, len(groupMap))
	for _, info := range groupMap {
		res = append(res, *info)
	}
	return res
}

// SavePoolConfig 保存全局号池调度与并发限制配置
func (s *AccountService) SavePoolConfig(cfg *model.PoolConfigDTO) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getTargetFilePath("accounts_pool.json")
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化号池配置失败: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("写入号池配置临时文件失败: %w", err)
	}

	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("持久化号池配置失败: %w", err)
	}

	return nil
}

// AddAccount 添加新账号
func (s *AccountService) AddAccount(req *model.AddAccountRequest) (*model.AccountDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = "nvidia"
	}

	fn := s.partitionFileName(provider)
	accounts, _ := s.readPartitionAccounts(fn)

	// 生成唯一 ID 与添加时间
	newID := fmt.Sprintf("%d-%05d", time.Now().UnixNano(), rand.Intn(100000))
	tier := req.Tier
	if tier == "" {
		if provider == "nvidia" {
			tier = "NVIDIA (第三方 API Key)"
		} else if provider == "other" {
			tier = "Other 自定义上游"
		} else if provider == "workbuddy" {
			tier = "Free"
		} else {
			tier = "Pro"
		}
	}

	newAcc := &model.AccountDTO{
		ID:           newID,
		Email:        strings.TrimSpace(req.Email),
		AccessToken:  strings.TrimSpace(req.AccessToken),
		Provider:     provider,
		ProjectID:    strings.TrimSpace(req.ProjectID),
		ProjectLabel: strings.TrimSpace(req.ProjectLabel),
		ScopeType:    provider,
		AddedAt:      time.Now().Format("2006-01-02T15:04:05+08:00"),
		Tier:         tier,
		Enabled:      true,
		BaseURL:      strings.TrimSpace(req.BaseURL),
		EgressIP:     strings.TrimSpace(req.EgressIP),
		DefaultModel: strings.TrimSpace(req.DefaultModel),
		ModelSonnet:  strings.TrimSpace(req.ModelSonnet),
		ModelOpus:    strings.TrimSpace(req.ModelOpus),
		ModelHaiku:   strings.TrimSpace(req.ModelHaiku),
		ModelFable:   strings.TrimSpace(req.ModelFable),
		GroupID:      strings.TrimSpace(req.GroupID),
		GroupName:    strings.TrimSpace(req.GroupName),
		Formats:      req.Formats,
	}

	// 默认 BaseURL 补齐
	if newAcc.BaseURL == "" {
		if provider == "nvidia" {
			newAcc.BaseURL = "https://integrate.api.nvidia.com/v1"
		} else if provider == "grok" {
			newAcc.BaseURL = "https://cli-chat-proxy.grok.com"
		} else if provider == "workbuddy" {
			newAcc.BaseURL = "https://www.codebuddy.ai"
		}
	}

	accounts = append([]*model.AccountDTO{newAcc}, accounts...)
	if err := s.writePartitionAccounts(fn, accounts); err != nil {
		return nil, err
	}

	newAcc.MaskedKey = maskKey(newAcc.AccessToken)
	return newAcc, nil
}

// UpdateAccount 编辑指定账号
func (s *AccountService) UpdateAccount(id string, req *model.UpdateAccountRequest) (*model.AccountDTO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	partitions := []string{"nvidia", "other", "grok", "workbuddy", "project", "antigravity"}
	for _, p := range partitions {
		fn := s.partitionFileName(p)
		accounts, _ := s.readPartitionAccounts(fn)

		for i, a := range accounts {
			if a.ID == id {
				if req.Email != "" {
					a.Email = strings.TrimSpace(req.Email)
				}
				if strings.TrimSpace(req.AccessToken) != "" {
					a.AccessToken = strings.TrimSpace(req.AccessToken)
					a.TokenRefreshedAt = time.Now().Unix()
				}
				if req.BaseURL != "" {
					a.BaseURL = strings.TrimSpace(req.BaseURL)
				}
				if req.GroupID != "" {
					a.GroupID = strings.TrimSpace(req.GroupID)
				}
				if req.GroupName != "" {
					a.GroupName = strings.TrimSpace(req.GroupName)
				}
				if len(req.Formats) > 0 {
					a.Formats = req.Formats
				}
				if req.DefaultModel != "" {
					a.DefaultModel = strings.TrimSpace(req.DefaultModel)
				}
				if req.ModelSonnet != "" {
					a.ModelSonnet = strings.TrimSpace(req.ModelSonnet)
				}
				if req.ModelOpus != "" {
					a.ModelOpus = strings.TrimSpace(req.ModelOpus)
				}
				if req.ModelHaiku != "" {
					a.ModelHaiku = strings.TrimSpace(req.ModelHaiku)
				}
				if req.ModelFable != "" {
					a.ModelFable = strings.TrimSpace(req.ModelFable)
				}
				if req.ProjectID != "" {
					a.ProjectID = strings.TrimSpace(req.ProjectID)
				}
				if req.ProjectLabel != "" {
					a.ProjectLabel = strings.TrimSpace(req.ProjectLabel)
				}
				if req.EgressIP != "" {
					a.EgressIP = strings.TrimSpace(req.EgressIP)
				}
				if req.Tier != "" {
					a.Tier = strings.TrimSpace(req.Tier)
				}
				if req.Enabled != nil {
					a.Enabled = *req.Enabled
				}

				accounts[i] = a
				if err := s.writePartitionAccounts(fn, accounts); err != nil {
					return nil, err
				}

				a.MaskedKey = maskKey(a.AccessToken)
				return a, nil
			}
		}
	}

	return nil, fmt.Errorf("未找到 ID 为 %s 的账号", id)
}

// ToggleAccount 快速启停单账号
func (s *AccountService) ToggleAccount(id string, enabled bool) error {
	_, err := s.UpdateAccount(id, &model.UpdateAccountRequest{
		Enabled: &enabled,
	})
	return err
}

// DeleteAccount 删除单个账号
func (s *AccountService) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	partitions := []string{"nvidia", "other", "grok", "workbuddy", "project", "antigravity"}
	for _, p := range partitions {
		fn := s.partitionFileName(p)
		accounts, _ := s.readPartitionAccounts(fn)

		newAccounts := make([]*model.AccountDTO, 0, len(accounts))
		found := false
		for _, a := range accounts {
			if a.ID == id {
				found = true
				continue
			}
			newAccounts = append(newAccounts, a)
		}

		if found {
			return s.writePartitionAccounts(fn, newAccounts)
		}
	}

	return fmt.Errorf("未找到 ID 为 %s 的账号", id)
}

// BatchDeleteAccounts 批量删除指定账号
func (s *AccountService) BatchDeleteAccounts(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[strings.TrimSpace(id)] = true
	}

	totalDeleted := 0
	partitions := []string{"nvidia", "other", "grok", "workbuddy", "project", "antigravity"}
	for _, p := range partitions {
		fn := s.partitionFileName(p)
		accounts, _ := s.readPartitionAccounts(fn)
		if len(accounts) == 0 {
			continue
		}

		newAccounts := make([]*model.AccountDTO, 0, len(accounts))
		deletedInPartition := 0
		for _, a := range accounts {
			if idSet[a.ID] {
				deletedInPartition++
				continue
			}
			newAccounts = append(newAccounts, a)
		}

		if deletedInPartition > 0 {
			totalDeleted += deletedInPartition
			if err := s.writePartitionAccounts(fn, newAccounts); err != nil {
				return totalDeleted, err
			}
		}
	}

	return totalDeleted, nil
}

// ImportAccounts 批量导入账号（自动按 provider 分类归档与去重）
func (s *AccountService) ImportAccounts(incoming []*model.AccountDTO) (int, error) {
	if len(incoming) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 按 provider 归类
	byProvider := make(map[string][]*model.AccountDTO)
	for _, acc := range incoming {
		p := strings.ToLower(strings.TrimSpace(acc.Provider))
		if p == "" {
			p = "nvidia"
		}
		if acc.ID == "" {
			acc.ID = fmt.Sprintf("%d-%05d", time.Now().UnixNano(), rand.Intn(100000))
		}
		if acc.AddedAt == "" {
			acc.AddedAt = time.Now().Format("2006-01-02T15:04:05+08:00")
		}
		byProvider[p] = append(byProvider[p], acc)
	}

	importedCount := 0
	for provider, accList := range byProvider {
		fn := s.partitionFileName(provider)
		existing, _ := s.readPartitionAccounts(fn)

		existingMap := make(map[string]bool)
		for _, a := range existing {
			if a.Email != "" {
				existingMap[a.Email] = true
			}
			if a.AccessToken != "" {
				existingMap[a.AccessToken] = true
			}
		}

		for _, item := range accList {
			// 简单去重判定（相同 Email 或相同 Token）
			if (item.Email != "" && existingMap[item.Email]) || (item.AccessToken != "" && existingMap[item.AccessToken]) {
				continue
			}
			existing = append([]*model.AccountDTO{item}, existing...)
			if item.Email != "" {
				existingMap[item.Email] = true
			}
			if item.AccessToken != "" {
				existingMap[item.AccessToken] = true
			}
			importedCount++
		}

		if err := s.writePartitionAccounts(fn, existing); err != nil {
			return importedCount, err
		}
	}

	return importedCount, nil
}

// ExportAccounts 导出指定通道或全部通道账号数据
func (s *AccountService) ExportAccounts(channel string) ([]*model.AccountDTO, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch != "" && ch != "all" {
		fn := s.partitionFileName(ch)
		accs, _ := s.readPartitionAccounts(fn)
		return accs, nil
	}

	var all []*model.AccountDTO
	partitions := []string{"antigravity", "project", "nvidia", "grok", "workbuddy", "other"}
	for _, p := range partitions {
		fn := s.partitionFileName(p)
		accs, _ := s.readPartitionAccounts(fn)
		all = append(all, accs...)
	}

	return all, nil
}
