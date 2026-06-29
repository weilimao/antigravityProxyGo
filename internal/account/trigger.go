package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
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
func TriggerTestResponse(ctx context.Context, acc *Account, modelName string, getStoredProject func(string) string, refreshToken func(*Account) (string, error)) error {
	// 1. Refresh access token
	accessToken, err := refreshToken(acc)
	if err != nil {
		return fmt.Errorf("refresh token failed: %w", err)
	}

	client := netutil.NewClient(15 * time.Second)

	if acc.Provider == "project" {
		// Vertex AI (Google Cloud Project)
		targetUrl := fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent", acc.ProjectID, modelName)
		
		reqBody := GeminiRequest{
			Contents: []Content{
				{
					Role: "user",
					Parts: []Part{{Text: "ok"}},
				},
			},
			GenerationConfig: GenerationConfig{MaxOutputTokens: 1},
		}

		jsonBytes, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("x-goog-user-project", acc.ProjectID)

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP error %d", resp.StatusCode)
		}
		return nil
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
					Parts: []Part{{Text: "ok"}},
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
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", targetUrl, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("User-Agent", "antigravity/1.21.9 darwin/arm64 google-api-nodejs-client/10.3.0")
		req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP error %d", resp.StatusCode)
		}
		return nil
	}
}
