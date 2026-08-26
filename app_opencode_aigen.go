package main

// app_opencode_aigen.go: OpenCode provider 的 AI 一键生成后端实现。
//
// 用途:用户在「Agent 配置 → OpenCode → 模型配置(provider)」卡片点「AI 生成」按钮,
// 选一个中继服务暴露的任意模型 + 一段可编辑提示词,后端用该模型调本地中继的
// OpenAI 兼容入口 /v1/chat/completions, 让 AI 输出一份合法的 provider.{name} JSON
// 片段回填到右侧 JSON 编辑器与左侧表单, 从而把任意第三方模型上游接入中继。
//
// 与 pricing/aigen.go 的区别:
//   - pricing 走 Gemini v1internal 信封直连 daily-cloudcode-pa(号池 token + google_search);
//   - 本实现走本地中继 OpenAI 兼容入口, 复用用户已配置的任意模型路由,
//     不绑死 Antigravity 号池, 不需要账号 token —— 中继自身负责鉴权与上游路由。
//
// 直连本地中继(127.0.0.1:relayPort)且显式不走系统代理:
//   netutil.NewClient 的 transport 绑了 GetSystemProxy, 在用户开 Clash 全局代理时
//   会把 127.0.0.1:18444 也塞进代理隧道导致自环失败。本实现用独立的 &http.Client{
//   Transport: &http.Transport{Proxy: nil}} 绕开, 仅用于本地回环调用。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// opencodeProviderRequest 是 /v1/chat/completions 的 OpenAI 兼容请求体。
type opencodeProviderRequest struct {
	Model    string                       `json:"model"`
	Messages []opencodeProviderChatMessage `json:"messages"`
	Stream   bool                         `json:"stream"`
}

type opencodeProviderChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// opencodeProviderResponse 是 /v1/chat/completions 非流式响应的精简结构,
// 只取 choices[0].message.content, 其余字段忽略。
type opencodeProviderResponse struct {
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

// localRelayClient 是直连本地中继、显式不走系统代理的 HTTP 客户端。
// 复用同一个 Transport 控制连接数, 避免每次生成新建连接池。
var localRelayClient = &http.Client{
	Timeout: 180 * time.Second, // AI 生成可能较慢, 给足 3 分钟
	Transport: &http.Transport{
		Proxy:               nil, // 显式不走任何代理, 127.0.0.1 必须直连
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	},
}

// generateOpenCodeProvider 调本地中继让 AI 生成一份 provider.{name} JSON 片段。
//
// 入参:
//   - model:         中继服务暴露的模型名(前端 ModelSearchSelect 选中), 不可空
//   - systemPrompt:  系统提示词(前端可编辑, 带默认模板), 不可空
//   - userInput:     用户补充的需求描述(可空, 如"接入 DeepSeek, 模型 deepseek-chat")
//   - selectedModels: 前端多选的真实模型列表(可空)。非空时注入 user prompt 强约束
//     AI 只能为这些模型生成条目 —— 防止 AI 凭训练知识编造已下架/不存在的模型 id。
//
// 返回 AI 输出的原始文本(预期是 JSON), 由前端 JSON.parse 校验后回填。
// 中继端口取 settingsMgr.GetRelayPort(), 默认 18444; 为空时报错而非猜端口。
func (a *App) generateOpenCodeProvider(model, systemPrompt, userInput string, selectedModels []string) (string, error) {
	if model == "" {
		return "", errors.New("未选择生成用的模型")
	}
	if systemPrompt == "" {
		return "", errors.New("系统提示词不能为空")
	}
	if a.settingsMgr == nil {
		return "", errors.New("设置管理器未就绪")
	}
	port := a.settingsMgr.GetRelayPort()
	// GetRelayPort 在 settings_manager.go 已设默认 "18444", 此处再兜底防漂移。
	if port == "" {
		port = "18444"
	}

	// 组装 messages: system = 用户可编辑的模板; user = 用户的补充需求(可空则给一句默认)
	messages := []opencodeProviderChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	userMsg := strings.TrimSpace(userInput)
	if userMsg == "" {
		userMsg = "请生成一份通用的 OpenCode provider 配置, 接入本地中继服务。"
	}
	// 用户多选了真实模型列表时, 追加强约束段落:
	// models 字段的 key 必须严格逐字等于列表中的模型 id, 一个不多一个不少。
	if len(selectedModels) > 0 {
		var b strings.Builder
		b.WriteString("\n\n【强约束】本次必须且只能为以下模型生成 models 条目,")
		b.WriteString("models 对象的 key 必须逐字等于下列模型 id(禁止改动、禁止增删、禁止使用任何不在列表中的模型 id):\n")
		for i, m := range selectedModels {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
		userMsg += b.String()
	}
	messages = append(messages, opencodeProviderChatMessage{Role: "user", Content: userMsg})

	reqBody := opencodeProviderRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}

	targetURL := fmt.Sprintf("http://127.0.0.1:%s/v1/chat/completions", port)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// 中继 /v1/* 入口强制鉴权(compat.go ServeHTTP): token 为空直接 401。
	// auth.go ValidateToken 对 "sk-" 前缀的 token 走官方 Key 自动映射:
	// 有启用的中继用户则挂到该用户, 否则落到默认本地管理员会话(defaultLocalAdminUserID)。
	// 桌面端自调用属本地回环, 用固定 sk-local-aigen 标识走 bypass 会话即可。
	req.Header.Set("Authorization", "Bearer sk-local-aigen")
	req.Header.Set("User-Agent", "antigravity-desktop/aigen")

	resp, err := localRelayClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用本地中继失败(%s): %w", targetURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// 探测 error.message 给更可读的错误
		var probe opencodeProviderResponse
		if json.Unmarshal(bodyBytes, &probe) == nil && probe.Error != nil && probe.Error.Message != "" {
			return "", fmt.Errorf("中继返回 HTTP %d: %s", resp.StatusCode, probe.Error.Message)
		}
		bodySnippet := string(bodyBytes)
		if len(bodySnippet) > 300 {
			bodySnippet = bodySnippet[:300]
		}
		return "", fmt.Errorf("中继返回 HTTP %d: %s", resp.StatusCode, bodySnippet)
	}

	var respData opencodeProviderResponse
	if err := json.Unmarshal(bodyBytes, &respData); err != nil {
		snippet := string(bodyBytes)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return "", fmt.Errorf("解析中继响应失败: %v, 原始: %s", err, snippet)
	}
	if len(respData.Choices) == 0 {
		return "", errors.New("AI 未返回任何内容(choices 为空)")
	}
	content := strings.TrimSpace(respData.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("AI 返回了空内容")
	}
	return content, nil
}

// getTopRelayModels 聚合中继全部用户的模型用量统计, 按请求次数降序返回模型名列表。
//
// 用途: AI 生成 provider 弹窗的「生成哪些模型」多选默认预选 Top N(用户要求取调用量前十),
// 避免 AI 凭训练知识编造已下架/不存在的模型 id。
//
// 数据源: relayStatsMgr.GetAllUsersStats() 的每用户 Models map (RelayModelStats.RequestCount),
// 同一模型跨用户聚合求和。映射列表里的模型若从未被调用不会出现在统计里, 属预期——
// 预选只覆盖"真正有流量"的模型, 其余仍可从完整映射列表手动勾选。
func (a *App) getTopRelayModels() []map[string]interface{} {
	if a.relayStatsMgr == nil {
		return []map[string]interface{}{}
	}
	type modelAgg struct {
		requests int
		inTok    int
		outTok   int
	}
	agg := map[string]*modelAgg{}
	for _, userStats := range a.relayStatsMgr.GetAllUsersStats() {
		if userStats == nil {
			continue
		}
		for name, ms := range userStats.Models {
			if ms == nil || name == "" {
				continue
			}
			entry, ok := agg[name]
			if !ok {
				entry = &modelAgg{}
				agg[name] = entry
			}
			entry.requests += ms.RequestCount
			entry.inTok += ms.InputTokens
			entry.outTok += ms.OutputTokens
		}
	}
	type sorted struct {
		name string
		agg  *modelAgg
	}
	list := make([]sorted, 0, len(agg))
	for name, entry := range agg {
		list = append(list, sorted{name: name, agg: entry})
	}
	// 请求次数优先, 次数相同按总 token 量兜底排序
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].agg, list[j].agg
		if a.requests != b.requests {
			return a.requests > b.requests
		}
		return a.inTok+a.outTok > b.inTok+b.outTok
	})
	result := make([]map[string]interface{}, 0, len(list))
	for _, item := range list {
		result = append(result, map[string]interface{}{
			"model":       item.name,
			"requests":    item.agg.requests,
			"inputTokens": item.agg.inTok,
			"outputTokens": item.agg.outTok,
		})
	}
	return result
}
