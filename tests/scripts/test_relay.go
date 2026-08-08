package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	url := "http://127.0.0.1:18444/v1internal:generateContent"
	apiKey := "sk-ant-9cd5fdbf9bf4109539272cf14ea643bb"

	// 构造符合 v1internal:generateContent 的 Payload 格式
	payload := map[string]interface{}{
		"model": "gemini-3.5-flash-extra-low", // 指定可用模型
		"request": map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"role": "user",
					"parts": []map[string]interface{}{
						{"text": "Hello, respond with success if you can read this."},
					},
				},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 1024,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("❌ JSON序列化失败: %v\n", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		fmt.Printf("❌ 创建请求失败: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	// 这里通过 Authorization 头传入 API Key
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "antigravity-client")

	client := &http.Client{Timeout: 15 * time.Second}

	// 诊断测试 1: 获取模型列表 (GET /v1/models)
	fmt.Println("----------------------------------------")
	fmt.Println("诊断测试 1: 尝试访问 GET /v1/models ...")
	modelsUrl := "http://127.0.0.1:18444/v1/models"
	reqModels, _ := http.NewRequest("GET", modelsUrl, nil)
	reqModels.Header.Set("Authorization", "Bearer "+apiKey)

	respModels, err := client.Do(reqModels)
	if err != nil {
		fmt.Printf("❌ 诊断测试 1 失败: 无法连接 (%v)\n", err)
	} else {
		defer respModels.Body.Close()
		body, _ := io.ReadAll(respModels.Body)
		fmt.Printf("状态码: %d\n", respModels.StatusCode)
		fmt.Printf("响应内容: %s\n", string(body))
	}
	fmt.Println("----------------------------------------")

	// 诊断测试 2: 尝试访问原本的 POST /v1internal:generateContent
	fmt.Println("诊断测试 2: 尝试访问 POST /v1internal:generateContent ...")
	fmt.Printf("正在连接中继服务: %s ...\n", url)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ 诊断测试 2 失败: 无法连接到中继服务 (%v)\n", err)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ 读取响应体失败: %v\n", err)
		return
	}

	fmt.Printf("\n===== 测试结果 =====\n")
	fmt.Printf("状态码: %d\n", resp.StatusCode)
	if resp.StatusCode == 200 {
		fmt.Println("✅ 成功连接并收到响应！")
	} else {
		fmt.Println("⚠️ 服务已响应，但状态码非 200。请检查 Key 或后台日志。")
	}
	fmt.Printf("响应内容:\n%s\n", string(respBody))
}
