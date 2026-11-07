package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"antigravity-proxy/internal/netutil"
)

type Part struct {
	Text string `json:"text"`
}

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type GenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

type GeminiRequest struct {
	Contents         []Content        `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig"`
	SessionID        string           `json:"sessionId,omitempty"`
}

var (
	triggerAntigravityEndpoint = "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent"
	triggerHTTPClient          *http.Client
)

// TriggerTestResponse sends a minimal generateContent request to Google/Vertex AI using the account credentials.
func TriggerTestResponse(ctx context.Context, acc *Account, modelName string, prompt string, getStoredProject func(string) string, refreshToken func(*Account) (string, error), getUserAgent ...func() string) (string, error) {
	// 1. 优先复用账号仍在有效期内的 AccessToken（距离上次刷新 <50 分钟或 JWT exp 剩余 >10 分钟），
	// 避免在多模型遍历时每个模型都强制请求一次 OAuth 刷新接口触发风控
	accessToken := ""
	needRefresh := true
	if acc != nil {
		if existingTok := strings.TrimSpace(acc.GetAccessToken()); existingTok != "" {
			nowSec := time.Now().Unix()
			if exp := acc.AccessTokenExp(); exp > 0 {
				if exp-nowSec > 600 {
					accessToken = existingTok
					needRefresh = false
				}
			} else if refreshedAt := acc.GetTokenRefreshedAt(); refreshedAt > 0 && nowSec-refreshedAt < 3000 {
				accessToken = existingTok
				needRefresh = false
			}
		}
	}
	if needRefresh && refreshToken != nil {
		tok, err := refreshToken(acc)
		if err != nil {
			return "", fmt.Errorf("refresh token failed: %w", err)
		}
		accessToken = tok
		if acc != nil && strings.TrimSpace(tok) != "" {
			acc.SetAccessToken(tok)
		}
	}

	testPrompt := prompt
	if testPrompt == "" {
		testPrompt = RandomKeepAlivePrompt()
	}

	accKey := "default"
	if acc != nil {
		if strings.TrimSpace(acc.ID) != "" {
			accKey = acc.ID
		} else if strings.TrimSpace(acc.Email) != "" {
			accKey = acc.Email
		}
	}

	client := triggerHTTPClient
	if client == nil {
		client = netutil.GetAccountClient(accKey, 15*time.Second)
	}

	if acc.Provider == "project" {
		// Vertex AI (Google Cloud Project)
		targetUrl := fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent", acc.ProjectID, modelName)
		
		reqBody := GeminiRequest{
			Contents: []Content{
				{
					Role:  "user",
					Parts: []Part{{Text: testPrompt}},
				},
			},
			GenerationConfig: GenerationConfig{MaxOutputTokens: 1},
		}

		jsonBytes, err := json.Marshal(reqBody)
		if err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return "", err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("x-goog-user-project", acc.ProjectID)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read response body failed: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && !needRefresh && refreshToken != nil {
			if tok, rErr := refreshToken(acc); rErr == nil && tok != "" {
				retryReq, rReqErr := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
				if rReqErr == nil {
					retryReq.Header.Set("Content-Type", "application/json")
					retryReq.Header.Set("Authorization", "Bearer "+tok)
					retryReq.Header.Set("x-goog-user-project", acc.ProjectID)
					if retryResp, rDoErr := client.Do(retryReq); rDoErr == nil {
						bodyBytes, _ = io.ReadAll(retryResp.Body)
						retryResp.Body.Close()
						resp = retryResp
					}
				}
			}
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
		}
		return extractTextFromGeminiResponse(bodyBytes), nil
	} else {
		// Antigravity (Official Account)
		targetUrl := triggerAntigravityEndpoint
		
		actualProjectId := acc.ProjectID
		if actualProjectId == "" && getStoredProject != nil {
			actualProjectId = getStoredProject(acc.Email)
		}
		if actualProjectId == "" && getStoredProject != nil {
			actualProjectId = getStoredProject("default")
		}
		if actualProjectId == "" {
			actualProjectId = "favorable-synapse-ttvcb"
		}

		reqBody := GeminiRequest{
			Contents: []Content{
				{
					Role:  "user",
					Parts: []Part{{Text: testPrompt}},
				},
			},
			GenerationConfig: GenerationConfig{MaxOutputTokens: 1},
			SessionID:        AccountSessionID(accKey, "keepalive"),
		}

		wrappedReq := map[string]interface{}{
			"project":            actualProjectId,
			"requestId":          NewAntigravityRequestID("agent"),
			"request":            reqBody,
			"model":              modelName,
			"userAgent":          "antigravity",
			"requestType":        "chat",
			"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
		}

		jsonBytes, err := json.Marshal(wrappedReq)
		if err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return "", err
		}

		ua := ""
		for _, fn := range getUserAgent {
			if fn != nil {
				if v := strings.TrimSpace(fn()); v != "" {
					ua = v
					break
				}
			}
		}
		if ua == "" {
			ua = FormatAntigravityUserAgent(DefaultAntigravityCliVersion)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("User-Agent", ua)

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read response body failed: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && !needRefresh && refreshToken != nil {
			if tok, rErr := refreshToken(acc); rErr == nil && tok != "" {
				retryReq, rReqErr := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
				if rReqErr == nil {
					retryReq.Header.Set("Content-Type", "application/json")
					retryReq.Header.Set("Authorization", "Bearer "+tok)
					retryReq.Header.Set("User-Agent", ua)
					if retryResp, rDoErr := client.Do(retryReq); rDoErr == nil {
						bodyBytes, _ = io.ReadAll(retryResp.Body)
						retryResp.Body.Close()
						resp = retryResp
					}
				}
			}
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
		}
		return extractTextFromGeminiResponse(bodyBytes), nil
	}
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type AntigravityWrappedResponse struct {
	Response GeminiResponse `json:"response"`
}

func extractTextFromGeminiResponse(body []byte) string {
	// 1. Try wrapped format first (e.g. Antigravity official proxy response)
	var wrapped AntigravityWrappedResponse
	if err := json.Unmarshal(body, &wrapped); err == nil {
		if len(wrapped.Response.Candidates) > 0 && len(wrapped.Response.Candidates[0].Content.Parts) > 0 {
			t := wrapped.Response.Candidates[0].Content.Parts[0].Text
			if t != "" {
				return t
			}
		}
	}

	// 2. Try standard Gemini response format
	var resp GeminiResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
			t := resp.Candidates[0].Content.Parts[0].Text
			if t != "" {
				return t
			}
		}
	}

	s := string(body)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	if len(s) > 100 {
		return s[:97] + "..."
	}
	return s
}
