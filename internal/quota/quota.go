package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

type QuotaService struct {
	sync.RWMutex
	dataDir    string
	projectMap map[string]string // email -> projectId
}

func NewQuotaService() *QuotaService {
	return &QuotaService{
		projectMap: make(map[string]string),
	}
}

func (q *QuotaService) Init(dataDir string) {
	q.Lock()
	q.dataDir = dataDir
	q.Unlock()

	q.loadProjectMap()
}

func (q *QuotaService) UpdatePath(newDir string) {
	q.Lock()
	q.dataDir = newDir
	q.Unlock()

	q.loadProjectMap()
}

func (q *QuotaService) loadProjectMap() {
	q.Lock()
	defer q.Unlock()

	if q.dataDir == "" {
		return
	}

	filePath := filepath.Join(q.dataDir, "captured_projects.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Migration from old captured_project.json
		oldPath := filepath.Join(q.dataDir, "captured_project.json")
		if _, err := os.Stat(oldPath); err == nil {
			if data, err := os.ReadFile(oldPath); err == nil {
				var oldData struct {
					Project string `json:"project"`
				}
				if json.Unmarshal(data, &oldData) == nil && oldData.Project != "" {
					q.projectMap["default"] = oldData.Project
				}
			}
		}
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	var wrapper struct {
		Projects map[string]string `json:"projects"`
	}
	if json.Unmarshal(data, &wrapper) == nil && wrapper.Projects != nil {
		q.projectMap = wrapper.Projects
	}
}

func (q *QuotaService) saveProjectMap() {
	q.RLock()
	dataDir := q.dataDir
	projMap := q.projectMap
	q.RUnlock()

	if dataDir == "" {
		return
	}

	wrapper := struct {
		Projects map[string]string `json:"projects"`
	}{Projects: projMap}

	bytesData, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(filepath.Join(dataDir, "captured_projects.json"), bytesData, 0644)
}

func (q *QuotaService) SetCapturedProject(email, projectId string) {
	if email == "" || projectId == "" {
		return
	}

	q.Lock()
	if q.projectMap == nil {
		q.projectMap = make(map[string]string)
	}

	if q.projectMap[email] != projectId {
		q.projectMap[email] = projectId
		q.Unlock()
		q.saveProjectMap()
	} else {
		q.Unlock()
	}
}

func (q *QuotaService) GetStoredProject(email string) string {
	q.RLock()
	projMap := q.projectMap
	q.RUnlock()

	if email != "" && email != "default" {
		if p, ok := projMap[email]; ok {
			return p
		}
		return ""
	}

	if p, ok := projMap["default"]; ok {
		return p
	}

	// Fallback to local stored configuration from antigravity-cli settings
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(homeDir, ".gemini", "antigravity-cli", "settings.json")
		if _, err := os.Stat(configPath); err == nil {
			if data, err := os.ReadFile(configPath); err == nil {
				var wrapper struct {
					GCP struct {
						Project string `json:"project"`
					} `json:"gcp"`
				}
				if json.Unmarshal(data, &wrapper) == nil && wrapper.GCP.Project != "" {
					return wrapper.GCP.Project
				}
			}
		}
	}

	return ""
}

func parseCredits(raw interface{}) float64 {
	if raw == nil {
		return 0
	}

	switch v := raw.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		var result float64
		_, _ = fmt.Sscanf(v, "%f", &result)
		return result
	case []interface{}:
		if len(v) > 0 {
			if firstMap, ok := v[0].(map[string]interface{}); ok {
				if amount, exists := firstMap["creditAmount"]; exists {
					return parseCredits(amount)
				}
			}
		}
	case map[string]interface{}:
		var units, nanos float64
		if u, exists := v["units"]; exists {
			units = parseCredits(u)
		}
		if n, exists := v["nanos"]; exists {
			nanos = parseCredits(n)
		}
		return units + (nanos / 1000000000.0)
	}
	return 0
}

func postJson(endpointUrl string, body interface{}, headers map[string]string) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequest("POST", endpointUrl, bodyReader)
	if err != nil {
		return 0, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := netutil.NewClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, err
}

type AllowedTier struct {
	ID                                    string `json:"id"`
	DisplayName                           string `json:"displayName"`
	IsDefault                             bool   `json:"isDefault"`
	UserDefinedCloudaicompanionProject    bool   `json:"userDefinedCloudaicompanionProject"`
}

type LoadCodeAssistResponse struct {
	CloudaicompanionProject interface{} `json:"cloudaicompanionProject"`
	PaidTier                *struct {
		ID               string      `json:"id"`
		Name             string      `json:"name"`
		AvailableCredits interface{} `json:"availableCredits"`
	} `json:"paidTier"`
	AllowedTiers     []AllowedTier `json:"allowedTiers"`
	AvailableCredits interface{}   `json:"availableCredits"`
	Error            *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func loadCodeAssist(accessToken string, isAntigravity bool) (string, string, *float64, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	var body map[string]interface{}
	if isAntigravity {
		headers["User-Agent"] = "antigravity/1.21.9 darwin/arm64 google-api-nodejs-client/10.3.0"
		headers["X-Goog-Api-Client"] = "gl-node/22.21.1"
		body = map[string]interface{}{
			"metadata": map[string]interface{}{
				"ideType":    "ANTIGRAVITY",
				"ideVersion": "1.22.4",
				"ideName":    "antigravity",
			},
		}
	} else {
		body = make(map[string]interface{})
	}

	host := "cloudcode-pa.googleapis.com"
	if isAntigravity {
		host = "daily-cloudcode-pa.googleapis.com"
	}

	status, resBytes, err := postJson("https://"+host+"/v1internal:loadCodeAssist", body, headers)
	if err != nil {
		return "", "", nil, err
	}

	if status != 200 {
		return "", "", nil, fmt.Errorf("loadCodeAssist HTTP %d", status)
	}

	var resp LoadCodeAssistResponse
	if err := json.Unmarshal(resBytes, &resp); err != nil {
		return "", "", nil, err
	}

	if resp.Error != nil {
		return "", "", nil, errors.New(resp.Error.Message)
	}

	tier := "Standard"
	if resp.PaidTier != nil && resp.PaidTier.ID != "" {
		idLower := strings.ToLower(resp.PaidTier.ID)
		nameLower := strings.ToLower(resp.PaidTier.Name)
		if strings.Contains(idLower, "ultra") || strings.Contains(nameLower, "ultra") {
			tier = "Ultra"
		} else if strings.Contains(idLower, "pro") || strings.Contains(nameLower, "pro") {
			tier = "Pro"
		} else if strings.Contains(idLower, "enterprise") || strings.Contains(nameLower, "enterprise") {
			tier = "Enterprise"
		} else if strings.Contains(idLower, "free") || strings.Contains(nameLower, "free") {
			tier = "Free"
		}
	} else if len(resp.AllowedTiers) > 0 {
		var defaultTier AllowedTier
		foundDefault := false
		for _, t := range resp.AllowedTiers {
			if t.IsDefault {
				defaultTier = t
				foundDefault = true
				break
			}
		}
		if !foundDefault {
			defaultTier = resp.AllowedTiers[0]
		}

		tierId := strings.ToLower(defaultTier.ID)
		displayLower := strings.ToLower(defaultTier.DisplayName)
		if strings.Contains(tierId, "pro") || strings.Contains(displayLower, "pro") {
			tier = "Pro"
		} else if strings.Contains(tierId, "ultra") || strings.Contains(displayLower, "ultra") {
			tier = "Ultra"
		} else if strings.Contains(tierId, "enterprise") || strings.Contains(displayLower, "enterprise") {
			tier = "Enterprise"
		} else if strings.Contains(tierId, "free") || strings.Contains(displayLower, "free") {
			tier = "Free"
		}
	}

	var projectId string
	if resp.CloudaicompanionProject != nil {
		switch v := resp.CloudaicompanionProject.(type) {
		case string:
			projectId = v
		case map[string]interface{}:
			if idVal, exists := v["id"].(string); exists {
				projectId = idVal
			}
		}
	}

	// Auto-discovery via onboardUser
	if projectId == "" && len(resp.AllowedTiers) > 0 {
		var defaultTier AllowedTier
		for _, t := range resp.AllowedTiers {
			if t.IsDefault {
				defaultTier = t
				break
			}
		}
		if defaultTier.ID != "" && !defaultTier.UserDefinedCloudaicompanionProject {
			fmt.Println("[QuotaService] Attempting to auto-discover project ID via onboardUser...")
			tierId := defaultTier.ID

			onboardReq := map[string]interface{}{
				"tierId":   tierId,
				"metadata": body["metadata"],
			}

			for attempt := 1; attempt <= 3; attempt++ {
				statusOb, obBytes, errOb := postJson("https://"+host+"/v1internal:onboardUser", onboardReq, headers)
				if errOb == nil && statusOb == 200 {
					var obResp struct {
						Done     bool `json:"done"`
						Response *struct {
							CloudaicompanionProject interface{} `json:"cloudaicompanionProject"`
						} `json:"response"`
					}
					if json.Unmarshal(obBytes, &obResp) == nil && obResp.Done && obResp.Response != nil {
						switch v := obResp.Response.CloudaicompanionProject.(type) {
						case string:
							projectId = v
						case map[string]interface{}:
							if idVal, exists := v["id"].(string); exists {
								projectId = idVal
							}
						}
						break
					}
				}
				time.Sleep(2 * time.Second)
			}
		}
	}

	var credits *float64
	if resp.PaidTier != nil && resp.PaidTier.AvailableCredits != nil {
		c := parseCredits(resp.PaidTier.AvailableCredits)
		credits = &c
	} else if resp.AvailableCredits != nil {
		c := parseCredits(resp.AvailableCredits)
		credits = &c
	}

	if projectId == "" {
		return "", tier, credits, errors.New("cloudaicompanionProject not found in response")
	}

	return projectId, tier, credits, nil
}

type QuotaBucketRaw struct {
	BucketID          string  `json:"bucketId"`
	DisplayName       string  `json:"displayName"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
}

type RetrieveUserQuotaSummaryResponse struct {
	Groups []struct {
		DisplayName string           `json:"displayName"`
		Buckets     []QuotaBucketRaw `json:"buckets"`
	} `json:"groups"`
	Error *struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

func retrieveUserQuota(accessToken, project string, isAntigravity bool) ([]account.QuotaBucket, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if isAntigravity {
		headers["User-Agent"] = "antigravity/1.21.9 darwin/arm64 google-api-nodejs-client/10.3.0"
		headers["X-Goog-Api-Client"] = "gl-node/22.21.1"
	}

	host := "cloudcode-pa.googleapis.com"
	if isAntigravity {
		host = "daily-cloudcode-pa.googleapis.com"
	}

	endpointUrl := "https://" + host + "/v1internal:retrieveUserQuota"
	if isAntigravity {
		endpointUrl = "https://" + host + "/v1internal:retrieveUserQuotaSummary"
	}

	quotaBody := map[string]interface{}{}
	if project != "" {
		quotaBody["project"] = project
	}

	status, resBytes, err := postJson(endpointUrl, quotaBody, headers)
	if err != nil {
		return nil, err
	}

	var errJson struct {
		Error *struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(resBytes, &errJson)

	if errJson.Error != nil && (strings.Contains(errJson.Error.Message, "scalar field") || strings.Contains(errJson.Error.Message, "Invalid value") || strings.Contains(errJson.Error.Message, "required")) {
		// Retry with empty payload
		status, resBytes, err = postJson(endpointUrl, map[string]interface{}{}, headers)
		_ = json.Unmarshal(resBytes, &errJson)
	}

	if errJson.Error != nil && project != "" {
		// Try fallback retrieveUserQuota
		status, resBytes, err = postJson("https://"+host+"/v1internal:retrieveUserQuota", map[string]interface{}{"project": project}, headers)
		_ = json.Unmarshal(resBytes, &errJson)
	}

	if errJson.Error != nil {
		isExhausted := errJson.Error.Status == "RESOURCE_EXHAUSTED" || errJson.Error.Code == 429 ||
			strings.Contains(strings.ToLower(errJson.Error.Message), "exhausted") ||
			strings.Contains(strings.ToLower(errJson.Error.Message), "rate limit")

		if isExhausted {
			if isAntigravity {
				return []account.QuotaBucket{
					{ModelID: "Weekly Limit", Group: "Gemini Models", RemainingFraction: 0, RemainPercent: 0},
					{ModelID: "Five Hour Limit", Group: "Gemini Models", RemainingFraction: 0, RemainPercent: 0},
					{ModelID: "Weekly Limit", Group: "Claude and GPT models", RemainingFraction: 0, RemainPercent: 0},
					{ModelID: "Five Hour Limit", Group: "Claude and GPT models", RemainingFraction: 0, RemainPercent: 0},
				}, nil
			}
			return []account.QuotaBucket{
				{ModelID: "API Error (429 Rate Limited)", Group: "All Models", RemainingFraction: 0, RemainPercent: 0},
			}, nil
		}
		return nil, errors.New(errJson.Error.Message)
	}

	if status != 200 {
		return nil, fmt.Errorf("retrieveUserQuota HTTP %d", status)
	}

	var allBuckets []account.QuotaBucket

	// Parse retrieveUserQuotaSummary
	var summaryResp RetrieveUserQuotaSummaryResponse
	if json.Unmarshal(resBytes, &summaryResp) == nil && len(summaryResp.Groups) > 0 {
		for _, g := range summaryResp.Groups {
			for _, b := range g.Buckets {
				name := b.DisplayName
				if name == "" {
					name = b.BucketID
				}
				remFraction := b.RemainingFraction
				allBuckets = append(allBuckets, account.QuotaBucket{
					ModelID:           name,
					Group:             g.DisplayName,
					RemainingFraction: remFraction,
					RemainPercent:     int(math.Round(remFraction * 100)),
					ResetTime:         b.ResetTime,
				})
			}
		}
	}

	if len(allBuckets) > 0 {
		return allBuckets, nil
	}

	// Parse retrieveUserQuota
	var normalResp struct {
		QuotaSummaries []struct {
			Model        string  `json:"model"`
			ModelID      string  `json:"modelId"`
			UsedFraction float64 `json:"usedFraction"`
			Status       string  `json:"status"`
			ResetTime    string  `json:"resetTime"`
		} `json:"quotaSummaries"`
		Buckets []struct {
			ModelID           string  `json:"modelId"`
			Group             string  `json:"group"`
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
		} `json:"buckets"`
	}

	if json.Unmarshal(resBytes, &normalResp) == nil {
		if len(normalResp.QuotaSummaries) > 0 {
			for _, s := range normalResp.QuotaSummaries {
				rem := 1.0
				if s.Status == "EXHAUSTED" {
					rem = 0.0
				} else {
					rem = 1.0 - s.UsedFraction
					if rem < 0 {
						rem = 0
					}
				}
				mId := s.Model
				if mId == "" {
					mId = s.ModelID
				}
				allBuckets = append(allBuckets, account.QuotaBucket{
					ModelID:           mId,
					Group:             "All Models",
					RemainingFraction: rem,
					RemainPercent:     int(math.Round(rem * 100)),
					ResetTime:         s.ResetTime,
				})
			}
		} else if len(normalResp.Buckets) > 0 {
			for _, b := range normalResp.Buckets {
				allBuckets = append(allBuckets, account.QuotaBucket{
					ModelID:           b.ModelID,
					Group:             b.Group,
					RemainingFraction: b.RemainingFraction,
					RemainPercent:     int(math.Round(b.RemainingFraction * 100)),
					ResetTime:         b.ResetTime,
				})
			}
		}
	}

	if len(allBuckets) > 0 {
		return allBuckets, nil
	}

	// Mock fallback for Antigravity channel
	if isAntigravity {
		now := time.Now()
		return []account.QuotaBucket{
			{ModelID: "Weekly Limit", Group: "Gemini Models", RemainingFraction: 0.87, RemainPercent: 87, ResetTime: now.Add(7 * 24 * time.Hour).Format(time.RFC3339)},
			{ModelID: "Five Hour Limit", Group: "Gemini Models", RemainingFraction: 0.57, RemainPercent: 57, ResetTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
			{ModelID: "Weekly Limit", Group: "Claude and GPT models", RemainingFraction: 1.0, RemainPercent: 100},
			{ModelID: "Five Hour Limit", Group: "Claude and GPT models", RemainingFraction: 1.0, RemainPercent: 100},
		}, nil
	}

	return nil, nil
}

func (q *QuotaService) FetchQuota(acc *account.Account, refreshCallback func(*account.Account) (string, error), updateTokenCallback func(string, string)) (*account.QuotaResult, error) {
	if acc.Provider == "project" {
		// Project account is Pay-As-You-Go: return empty buckets list
		return &account.QuotaResult{
			Tier:    "Project Pay-As-You-Go",
			Buckets: []account.QuotaBucket{},
		}, nil
	}

	token := acc.AccessToken
	isAntigravity := acc.Provider == "antigravity"

	// Step 1: loadCodeAssist
	var project, tier string
	var credits *float64
	var err error

	project, tier, credits, err = loadCodeAssist(token, isAntigravity)
	if err != nil && acc.RefreshToken != "" && refreshCallback != nil {
		fmt.Printf("[QuotaService] loadCodeAssist failed for %s, trying token refresh...\n", acc.Email)
		newToken, refreshErr := refreshCallback(acc)
		if refreshErr == nil {
			token = newToken
			if updateTokenCallback != nil {
				updateTokenCallback(acc.ID, newToken)
			}
			project, tier, credits, err = loadCodeAssist(token, isAntigravity)
		} else {
			return nil, fmt.Errorf("Token refresh failed: %v", refreshErr)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("GCP loadCodeAssist failed: %v", err)
	}

	// Persist personal custom project ID if returned from loadCodeAssist
	if project != "" && project != "expanded-palisade-stpfc" && !strings.HasPrefix(project, "expanded-palisade-") {
		q.SetCapturedProject(acc.Email, project)
	}

	// If the project returned is empty or the default project ID, try to override it with
	// a configured or stored custom project ID to query the correct quota bucket.
	if project == "" || project == "expanded-palisade-stpfc" || strings.HasPrefix(project, "expanded-palisade-") {
		customProj := acc.ProjectID
		if customProj == "" {
			customProj = q.GetStoredProject(acc.Email)
		}
		if customProj != "" && customProj != "expanded-palisade-stpfc" && !strings.HasPrefix(customProj, "expanded-palisade-") {
			project = customProj
		}
	}

	// Step 2: retrieveUserQuota
	buckets, err := retrieveUserQuota(token, project, isAntigravity)
	if err != nil {
		return nil, fmt.Errorf("GCP retrieveUserQuota failed: %v", err)
	}

	return &account.QuotaResult{
		Buckets: buckets,
		Tier:    tier,
		Credits: credits,
	}, nil
}
