// Package pricing: aigen_model.go —— 基于通用模型的 AI 一键生成计费配置实现。
//
// 用途:用户在「计费配置」选择「按模型生成」时,前端经公共 ModelSearchSelect 选中
// 一个中继服务暴露的模型,调本地中继 /v1/chat/completions 接口让该模型生成定价建议。
// 响应内容经 extractPricingJSON 与 buildResultMap 统一解析成结构化 AIPriceResult。
package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// modelChatRequest 是 /v1/chat/completions 的 OpenAI 兼容请求体。
type modelChatRequest struct {
	Model    string             `json:"model"`
	Messages []modelChatMessage `json:"messages"`
	Stream   bool               `json:"stream"`
}

// modelChatMessage 是聊天消息体。
type modelChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// modelChatResponse 是非流式响应的解析结构。
type modelChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// GenerateByModel 调用指定的通用模型(经本地中继 /v1/chat/completions)为一批模型生成单价建议。
//
// 入参:
//   - ctx:         上下文,控制超时与取消
//   - client:      HTTP 客户端,若为 nil 则使用默认 180s 超时客户端
//   - endpointURL: 中继 /v1/chat/completions 完整端点(如 http://127.0.0.1:18444/v1/chat/completions)
//   - model:       生成用的模型名称(如 gemini-2.5-flash, deepseek-chat, gpt-4o 等)
//   - models:      待生成建议单价的目标模型基名列表
//   - progressFn:  进度阶段回调,在调用与解析边界触发
func GenerateByModel(
	ctx context.Context,
	client *http.Client,
	endpointURL string,
	model string,
	models []string,
	progressFn func(stage, status string),
) (map[string]AIPriceResult, error) {
	result := make(map[string]AIPriceResult, len(models))
	if len(models) == 0 {
		return result, nil
	}
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		if progressFn != nil {
			progressFn("error", "未选择生成用的模型")
		}
		return result, errors.New("未选择生成用的模型")
	}
	trimmedEndpoint := strings.TrimSpace(endpointURL)
	if trimmedEndpoint == "" {
		if progressFn != nil {
			progressFn("error", "中继端点未就绪")
		}
		return result, errors.New("中继端点未就绪")
	}

	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second}
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
	}

	if progressFn != nil {
		progressFn("calling-model", fmt.Sprintf("正在调用模型 %s 生成定价...", trimmedModel))
	}

	prompt := BuildPricingPrompt(models)
	reqBody := modelChatRequest{
		Model: trimmedModel,
		Messages: []modelChatMessage{
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		if progressFn != nil {
			progressFn("error", "序列化请求失败: "+err.Error())
		}
		return result, fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trimmedEndpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		if progressFn != nil {
			progressFn("error", "创建请求失败: "+err.Error())
		}
		return result, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// 携带本地管理员回环凭据，通过本地中继鉴权
	req.Header.Set("Authorization", "Bearer sk-local-aigen")
	req.Header.Set("User-Agent", "antigravity-desktop/aigen")

	resp, err := client.Do(req)
	if err != nil {
		if progressFn != nil {
			progressFn("error", "请求模型失败: "+err.Error())
		}
		return result, fmt.Errorf("调用模型生成失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		if progressFn != nil {
			progressFn("error", "读取响应失败: "+readErr.Error())
		}
		return result, fmt.Errorf("读取模型响应失败: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		var probe modelChatResponse
		if json.Unmarshal(bodyBytes, &probe) == nil && probe.Error != nil && probe.Error.Message != "" {
			errStr := fmt.Sprintf("模型生成失败 (HTTP %d): %s", resp.StatusCode, probe.Error.Message)
			if progressFn != nil {
				progressFn("error", errStr)
			}
			return result, errors.New(errStr)
		}
		snippet := string(bodyBytes)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		errStr := fmt.Sprintf("模型生成失败 (HTTP %d): %s", resp.StatusCode, snippet)
		if progressFn != nil {
			progressFn("error", errStr)
		}
		return result, errors.New(errStr)
	}

	var chatResp modelChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		if progressFn != nil {
			progressFn("error", "解析模型响应 JSON 失败")
		}
		return result, fmt.Errorf("解析模型响应失败: %w", err)
	}

	if len(chatResp.Choices) == 0 || strings.TrimSpace(chatResp.Choices[0].Message.Content) == "" {
		if progressFn != nil {
			progressFn("error", "模型未返回任何有效内容")
		}
		return result, errors.New("模型未返回任何有效内容")
	}

	if progressFn != nil {
		progressFn("parse-result", "正在解析定价结果...")
	}

	content := chatResp.Choices[0].Message.Content
	extracted := extractPricingJSON(content)
	// 模型模式无 grounding 来源, 传空切片与 grounded=false; 内部若模型返回 source_url 会自动并入 Sources
	result = buildResultMap(models, extracted, nil, false)

	if progressFn != nil {
		progressFn("done", "生成完成")
	}

	return result, nil
}
