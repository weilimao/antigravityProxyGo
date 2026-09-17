package model

// AccountDTO 是向前端和外部接口暴露的账号数据载体
type AccountDTO struct {
	ID               string           `json:"id"`
	Email            string           `json:"email"`
	AccessToken      string           `json:"access_token,omitempty"`
	RefreshToken     string           `json:"refresh_token,omitempty"`
	Provider         string           `json:"provider"`
	ProjectID        string           `json:"projectId,omitempty"`
	ProjectLabel     string           `json:"projectLabel,omitempty"`
	ScopeType        string           `json:"scopeType,omitempty"`
	AddedAt          string           `json:"addedAt,omitempty"`
	Tier             string           `json:"tier,omitempty"`
	Enabled          bool             `json:"enabled"`
	EnableOverages   bool             `json:"enableOverages,omitempty"`
	Credits          *float64         `json:"credits,omitempty"`
	MaskedKey        string           `json:"maskedKey,omitempty"`
	BaseURL          string           `json:"baseUrl,omitempty"`
	EgressIP         string           `json:"egressIp,omitempty"`
	TokenEndpoint    string           `json:"tokenEndpoint,omitempty"`
	DefaultModel     string           `json:"defaultModel,omitempty"`
	ModelSonnet      string           `json:"modelSonnet,omitempty"`
	ModelOpus        string           `json:"modelOpus,omitempty"`
	ModelHaiku       string           `json:"modelHaiku,omitempty"`
	ModelFable       string           `json:"modelFable,omitempty"`
	GroupID          string           `json:"groupId,omitempty"`
	GroupName        string           `json:"groupName,omitempty"`
	Formats          []string         `json:"formats,omitempty"`
	CooldownUntil    int64            `json:"cooldownUntil,omitempty"`
	Cooldowns        map[string]int64 `json:"cooldowns,omitempty"`
	TokenRefreshedAt int64            `json:"token_refreshed_at,omitempty"`
}

// PoolConfigDTO 是号池全局与各通道的调度算法、并发数与控制配置
type PoolConfigDTO struct {
	PoolMode                  bool              `json:"poolMode"`
	ProjectPoolMode           bool              `json:"projectPoolMode"`
	GeminiCliPoolMode         bool              `json:"geminiCliPoolMode"`
	ActiveChannel             string            `json:"activeChannel"`
	OtherLBModes              map[string]string `json:"otherLbModes"`
	NvidiaLBMode              string            `json:"nvidiaLbMode"`
	GrokLBMode                string            `json:"grokLbMode"`
	NvidiaMaxConcurrency      int               `json:"nvidiaMaxConcurrency"`
	AntigravityMaxConcurrency int               `json:"antigravityMaxConcurrency"`
	AntigravityCliVersion     string            `json:"antigravityCliVersion"`
	ProjectMaxConcurrency     int               `json:"projectMaxConcurrency"`
	OtherMaxConcurrency       map[string]int    `json:"otherMaxConcurrency"`
	OtherWorkerProxyURLs      map[string]string `json:"otherWorkerProxyUrls,omitempty"`
	OtherWorkerProxyEnabled   map[string]bool   `json:"otherWorkerProxyEnabled,omitempty"`
	GrokMaxConcurrency        int               `json:"grokMaxConcurrency"`
	GrokCliVersion            string            `json:"grokCliVersion"`
	GrokQuotaCooldownHours    int               `json:"grokQuotaCooldownHours"`
	WorkbuddyLBMode           string            `json:"workbuddyLbMode,omitempty"`
	WorkbuddyMaxConcurrency   int               `json:"workbuddyMaxConcurrency,omitempty"`
}

// OtherGroupInfo 是 Other 号池下各上游渠道的元信息
type OtherGroupInfo struct {
	GroupID      string   `json:"groupId"`
	GroupName    string   `json:"groupName"`
	Formats      []string `json:"formats"`
	AccountCount int      `json:"accountCount"`
	EnabledCount int      `json:"enabledCount"`
}

// AccountsDataResponse 是前端拉取号池全量数据时的标准返回
type AccountsDataResponse struct {
	Accounts []*AccountDTO    `json:"accounts"`
	Config   *PoolConfigDTO   `json:"config"`
	Groups   []OtherGroupInfo `json:"otherGroups"`
}

// AddAccountRequest 新增账号请求载荷
type AddAccountRequest struct {
	Provider     string   `json:"provider" binding:"required"`
	Email        string   `json:"email" binding:"required"`
	AccessToken  string   `json:"access_token"`
	BaseURL      string   `json:"baseUrl"`
	GroupID      string   `json:"groupId"`
	GroupName    string   `json:"groupName"`
	Formats      []string `json:"formats"`
	DefaultModel string   `json:"defaultModel"`
	ModelSonnet  string   `json:"modelSonnet"`
	ModelOpus    string   `json:"modelOpus"`
	ModelHaiku   string   `json:"modelHaiku"`
	ModelFable   string   `json:"modelFable"`
	ProjectID    string   `json:"projectId"`
	ProjectLabel string   `json:"projectLabel"`
	EgressIP     string   `json:"egressIp"`
	Tier         string   `json:"tier"`
}

// UpdateAccountRequest 修改账号请求载荷
type UpdateAccountRequest struct {
	Email        string   `json:"email"`
	AccessToken  string   `json:"access_token"` // 若为空则保持原 Key 不变
	BaseURL      string   `json:"baseUrl"`
	GroupID      string   `json:"groupId"`
	GroupName    string   `json:"groupName"`
	Formats      []string `json:"formats"`
	DefaultModel string   `json:"defaultModel"`
	ModelSonnet  string   `json:"modelSonnet"`
	ModelOpus    string   `json:"modelOpus"`
	ModelHaiku   string   `json:"modelHaiku"`
	ModelFable   string   `json:"modelFable"`
	ProjectID    string   `json:"projectId"`
	ProjectLabel string   `json:"projectLabel"`
	EgressIP     string   `json:"egressIp"`
	Tier         string   `json:"tier"`
	Enabled      *bool    `json:"enabled"`
}

// BatchDeleteAccountsRequest 批量删除请求
type BatchDeleteAccountsRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// ImportAccountsRequest 导入账号请求
type ImportAccountsRequest struct {
	Accounts []*AccountDTO `json:"accounts" binding:"required"`
}
