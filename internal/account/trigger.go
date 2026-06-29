package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
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
}

// TriggerTestResponse sends a minimal generateContent request to Google/Vertex AI using the account credentials.
func TriggerTestResponse(ctx context.Context, acc *Account, modelName string, prompt string, getStoredProject func(string) string, refreshToken func(*Account) (string, error)) (string, error) {
	// 1. Refresh access token
	accessToken, err := refreshToken(acc)
	if err != nil {
		return "", fmt.Errorf("refresh token failed: %w", err)
	}

	testPrompt := prompt
	if testPrompt == "" {
		testPrompt = "ok"
	}

	client := netutil.NewClient(15 * time.Second)

	if acc.Provider == "project" {
		// Vertex AI (Google Cloud Project)
		targetUrl := fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent", acc.ProjectID, modelName)
		
		reqBody := GeminiRequest{
			Contents: []Content{
				{
					Role: "user",
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
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read response body failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
		}
		return extractTextFromGeminiResponse(bodyBytes), nil
	} else {
		// Antigravity (Official Account)
		targetUrl := "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent"
		
		actualProjectId := acc.ProjectID
		if actualProjectId == "" {
			actualProjectId = getStoredProject(acc.Email)
		}
		if actualProjectId == "" {
			actualProjectId = getStoredProject("default")
		}
		if actualProjectId == "" {
			actualProjectId = "expanded-palisade-stpfc"
		}

		reqBody := GeminiRequest{
			Contents: []Content{
				{
					Role: "user",
					Parts: []Part{{Text: testPrompt}},
				},
			},
			GenerationConfig: GenerationConfig{MaxOutputTokens: 1},
		}

		wrappedReq := map[string]interface{}{
			"project":   actualProjectId,
			"requestId": fmt.Sprintf("chat/%d-%d", time.Now().Unix(), rand.Intn(1000000)),
			"request":   reqBody,
			"model":     modelName,
			"userAgent": "antigravity",
			"requestType": "chat",
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

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("User-Agent", "antigravity/1.21.9 darwin/arm64 google-api-nodejs-client/10.3.0")
		req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read response body failed: %w", err)
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
