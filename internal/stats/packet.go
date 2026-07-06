package stats

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/netutil"
)

type CapturedPacket struct {
	ID         string      `json:"id"`
	Timestamp  string      `json:"timestamp"`
	Method     string      `json:"method"`
	Host       string      `json:"host"`
	Path       string      `json:"path"`
	URL        string      `json:"url"`
	ReqHeaders interface{} `json:"reqHeaders"`
	ReqBody    interface{} `json:"reqBody"`
	ResHeaders interface{} `json:"resHeaders"`
	ResBody    interface{} `json:"resBody"`
	StatusCode int         `json:"statusCode"`
	Source     string      `json:"source"`
}

type PacketCapturer struct {
	sync.RWMutex
	persistPath         string
	packets             []*CapturedPacket
	getAccountTokens    func(id string) (string, string, string, error) // returns token, refreshToken, projectId, error
	refreshAccount      func(id string) (string, error)
	enablePacketCapture func() bool
	saveTimeout         *time.Timer
	saveTimeoutLock     sync.Mutex
}

func NewPacketCapturer(
	getAccountTokens func(id string) (string, string, string, error),
	refreshAccount func(id string) (string, error),
	enablePacketCapture func() bool,
) *PacketCapturer {
	return &PacketCapturer{
		packets:             make([]*CapturedPacket, 0),
		getAccountTokens:    getAccountTokens,
		refreshAccount:      refreshAccount,
		enablePacketCapture: enablePacketCapture,
	}
}

func (pc *PacketCapturer) Init(userDataPath string) {
	pc.Lock()
	pc.persistPath = filepath.Join(userDataPath, "captured_packets.json")
	pc.Unlock()

	pc.LoadFromDisk()
}

func (pc *PacketCapturer) UpdatePath(newPath string) {
	pc.SaveToDisk()

	pc.Lock()
	pc.persistPath = filepath.Join(newPath, "captured_packets.json")
	pc.Unlock()

	pc.LoadFromDisk()
}

func (pc *PacketCapturer) SaveToDisk() {
	pc.RLock()
	path := pc.persistPath
	if path == "" {
		pc.RUnlock()
		return
	}
	data := pc.packets
	pc.RUnlock()

	bytesData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Printf("[PacketCapturer] Failed to marshal packets: %v\n", err)
		return
	}

	err = os.WriteFile(path, bytesData, 0644)
	if err != nil {
		fmt.Printf("[PacketCapturer] Failed to write packets: %v\n", err)
	}
}

func (pc *PacketCapturer) LoadFromDisk() {
	pc.Lock()
	defer pc.Unlock()

	if pc.persistPath == "" {
		pc.packets = make([]*CapturedPacket, 0)
		return
	}

	if _, err := os.Stat(pc.persistPath); os.IsNotExist(err) {
		pc.packets = make([]*CapturedPacket, 0)
		return
	}

	data, err := os.ReadFile(pc.persistPath)
	if err != nil {
		pc.packets = make([]*CapturedPacket, 0)
		return
	}

	if err := json.Unmarshal(data, &pc.packets); err != nil {
		pc.packets = make([]*CapturedPacket, 0)
		return
	}
}

func (pc *PacketCapturer) scheduleSave() {
	pc.saveTimeoutLock.Lock()
	defer pc.saveTimeoutLock.Unlock()

	if pc.saveTimeout != nil {
		return
	}

	pc.saveTimeout = time.AfterFunc(3*time.Second, func() {
		pc.SaveToDisk()
		pc.saveTimeoutLock.Lock()
		pc.saveTimeout = nil
		pc.saveTimeoutLock.Unlock()
	})
}


func (pc *PacketCapturer) GetPacketKey(method, host, urlPath string) string {
	cleanPath := strings.Split(urlPath, "?")[0]
	if cleanPath == "" {
		cleanPath = "/"
	}
	return strings.ToUpper(method) + " " + host + cleanPath
}

func (pc *PacketCapturer) IsCaptured(method, host, urlPath string) bool {
	pc.RLock()
	defer pc.RUnlock()

	targetKey := pc.GetPacketKey(method, host, urlPath)
	for _, p := range pc.packets {
		if pc.GetPacketKey(p.Method, p.Host, p.Path) == targetKey {
			return true
		}
	}
	return false
}

func (pc *PacketCapturer) SavePacket(method, host, urlPath string, reqHeaders map[string][]string, reqBody []byte, resHeaders map[string][]string, resBody []byte, statusCode int) *CapturedPacket {
	if pc.enablePacketCapture != nil && !pc.enablePacketCapture() {
		return nil
	}

	pc.Lock()
	defer pc.Unlock()

	cleanPath := strings.Split(urlPath, "?")[0]
	if cleanPath == "" {
		cleanPath = "/"
	}
	targetKey := pc.GetPacketKey(method, host, cleanPath)

	var parsedReqBody interface{}
	if len(reqBody) > 0 {
		if len(reqBody) > 50000 {
			parsedReqBody = string(reqBody[:50000]) + "... [已截断，防止内存溢出]"
		} else if json.Valid(reqBody) {
			parsedReqBody = json.RawMessage(reqBody)
		} else {
			parsedReqBody = string(reqBody)
		}
	}

	var parsedResBody interface{}
	if len(resBody) > 0 {
		isGzip := false
		for k, values := range resHeaders {
			if strings.ToLower(k) == "content-encoding" {
				for _, v := range values {
					if strings.Contains(strings.ToLower(v), "gzip") {
						isGzip = true
						break
					}
				}
			}
		}

		decompressedResBody := resBody
		if isGzip {
			reader, err := gzip.NewReader(bytes.NewReader(resBody))
			if err == nil {
				defer reader.Close()
				decompressed, errRead := io.ReadAll(reader)
				if errRead == nil {
					decompressedResBody = decompressed
				}
			}
		}

		if len(decompressedResBody) > 50000 {
			parsedResBody = string(decompressedResBody[:50000]) + "... [已截断，防止内存溢出]"
		} else if json.Valid(decompressedResBody) {
			parsedResBody = json.RawMessage(decompressedResBody)
		} else {
			parsedResBody = string(decompressedResBody)
		}
	}

	cleanHeaders := func(headers map[string][]string) map[string]string {
		res := make(map[string]string)
		sensitiveKeys := map[string]bool{
			"authorization":   true,
			"cookie":          true,
			"x-goog-api-key":  true,
			"api-key":         true,
			"proxy-auth":      true,
			"proxy-cookie":    true,
		}
		for k, values := range headers {
			lowerK := strings.ToLower(k)
			if sensitiveKeys[lowerK] {
				res[k] = "[REDACTED]"
			} else if len(values) > 0 {
				res[k] = values[0]
			}
		}
		return res
	}

	ua := ""
	for k, values := range reqHeaders {
		if strings.ToLower(k) == "user-agent" && len(values) > 0 {
			ua = values[0]
			break
		}
	}

	source := "未知"
	uaLower := strings.ToLower(ua)
	if strings.Contains(uaLower, "antigravity/cli") || strings.Contains(uaLower, "aidev_client") {
		source = "CLI"
	} else if strings.Contains(uaLower, "antigravity/ide") || strings.Contains(uaLower, "cloudaicompanion") || strings.Contains(uaLower, "google-api-nodejs-client") || strings.Contains(uaLower, "go-http-client") {
		source = "IDE"
	} else if strings.Contains(uaLower, "antigravity/hub") || strings.Contains(uaLower, "antigravityproxy-") {
		source = "Agent"
	}

	now := time.Now()
	timestamp := fmt.Sprintf("%02d/%02d %02d:%02d:%02d", now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())

	packet := &CapturedPacket{
		ID:         fmt.Sprintf("%d-%d", now.UnixNano(), rand.Intn(1000)),
		Timestamp:  timestamp,
		Method:     strings.ToUpper(method),
		Host:       host,
		Path:       cleanPath,
		URL:        "https://" + host + urlPath,
		ReqHeaders: cleanHeaders(reqHeaders),
		ReqBody:    parsedReqBody,
		ResHeaders: cleanHeaders(resHeaders),
		ResBody:    parsedResBody,
		StatusCode: statusCode,
		Source:     source,
	}

	existingIdx := -1
	for i, p := range pc.packets {
		if pc.GetPacketKey(p.Method, p.Host, p.Path) == targetKey {
			existingIdx = i
			break
		}
	}

	if existingIdx > -1 {
		pc.packets = append(pc.packets[:existingIdx], pc.packets[existingIdx+1:]...)
	}
	pc.packets = append([]*CapturedPacket{packet}, pc.packets...)

	// Truncate to maximum length of 50 to prevent unbounded memory growth
	if len(pc.packets) > 50 {
		pc.packets = pc.packets[:50]
	}

	// Trigger async disk save with debounce
	pc.scheduleSave()

	return packet
}

func (pc *PacketCapturer) GetPackets() []*CapturedPacket {
	pc.RLock()
	defer pc.RUnlock()
	return pc.packets
}

func (pc *PacketCapturer) ClearPackets() {
	pc.Lock()
	pc.packets = make([]*CapturedPacket, 0)
	pc.Unlock()
	pc.SaveToDisk()
}

func extractFieldPaths(obj interface{}, prefix string) []string {
	if obj == nil {
		return nil
	}

	var paths []string
	switch val := obj.(type) {
	case map[string]interface{}:
		for k, v := range val {
			curr := k
			if prefix != "" {
				curr = prefix + "." + k
			}
			paths = append(paths, curr)
			paths = append(paths, extractFieldPaths(v, curr)...)
		}
	case []interface{}:
		curr := prefix + "[]"
		if prefix == "" {
			curr = "[]"
		}
		paths = append(paths, curr)

		// Merge keys of objects in list to avoid duplicates
		mergedObj := make(map[string]interface{})
		hasObjects := false
		for _, item := range val {
			if itemMap, ok := item.(map[string]interface{}); ok {
				hasObjects = true
				for k, v := range itemMap {
					mergedObj[k] = v
				}
			}
		}
		if hasObjects {
			paths = append(paths, extractFieldPaths(mergedObj, curr)...)
		}
	}

	// Deduplicate
	dedupMap := make(map[string]bool)
	var uniquePaths []string
	for _, p := range paths {
		if !dedupMap[p] {
			dedupMap[p] = true
			uniquePaths = append(uniquePaths, p)
		}
	}
	sort.Strings(uniquePaths)
	return uniquePaths
}

func smartTruncateJson(val interface{}, maxStrLen int) interface{} {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case string:
		if len(v) > maxStrLen {
			return v[:maxStrLen] + fmt.Sprintf("... [已截断，原长度: %d]", len(v))
		}
		return v
	case map[string]interface{}:
		res := make(map[string]interface{})
		for k, valItem := range v {
			res[k] = smartTruncateJson(valItem, maxStrLen)
		}
		return res
	case []interface{}:
		if len(v) == 0 {
			return v
		}

		isPrimitiveArray := true
		for _, item := range v {
			if item != nil {
				if _, isMap := item.(map[string]interface{}); isMap {
					isPrimitiveArray = false
					break
				}
				if _, isSlice := item.([]interface{}); isSlice {
					isPrimitiveArray = false
					break
				}
			}
		}

		if isPrimitiveArray {
			if len(v) > 5 {
				head := v[:3]
				var newHead []interface{}
				for _, h := range head {
					newHead = append(newHead, smartTruncateJson(h, maxStrLen))
				}
				return append(newHead, fmt.Sprintf("... [已省略其余 %d 个元素]", len(v)-3))
			}
			var res []interface{}
			for _, item := range v {
				res = append(res, smartTruncateJson(item, maxStrLen))
			}
			return res
		}

		limit := len(v)
		if limit > 2 {
			limit = 2
		}
		var truncatedArray []interface{}
		for i := 0; i < limit; i++ {
			truncatedArray = append(truncatedArray, smartTruncateJson(v[i], maxStrLen))
		}
		if len(v) > limit {
			truncatedArray = append(truncatedArray, fmt.Sprintf("... [已省略其余 %d 个同结构元素以节省 Token]", len(v)-limit))
		}
		return truncatedArray
	default:
		return v
	}
}

func (pc *PacketCapturer) AnalyzePackets(accountId string, sourceType string) (string, error) {
	pc.RLock()
	if len(pc.packets) == 0 {
		pc.RUnlock()
		return "", errors.New("当前抓包日志为空，请先发起一些 API 请求！")
	}

	var targets []*CapturedPacket
	for _, p := range pc.packets {
		source := p.Source
		if source == "" {
			ua := ""
			if reqHeadersMap, ok := p.ReqHeaders.(map[string]interface{}); ok {
				for k, v := range reqHeadersMap {
					if strings.ToLower(k) == "user-agent" {
						if valStr, ok := v.(string); ok {
							ua = valStr
						}
						break
					}
				}
			} else if reqHeadersMapString, ok := p.ReqHeaders.(map[string]string); ok {
				for k, v := range reqHeadersMapString {
					if strings.ToLower(k) == "user-agent" {
						ua = v
						break
					}
				}
			}
			uaLower := strings.ToLower(ua)
			if strings.Contains(uaLower, "antigravity/cli") || strings.Contains(uaLower, "aidev_client") {
				source = "CLI"
			} else if strings.Contains(uaLower, "antigravity/ide") || strings.Contains(uaLower, "cloudaicompanion") || strings.Contains(uaLower, "google-api-nodejs-client") || strings.Contains(uaLower, "go-http-client") {
				source = "IDE"
			} else if strings.Contains(uaLower, "antigravity/hub") || strings.Contains(uaLower, "antigravityproxy-") {
				source = "Agent"
			} else {
				source = "未知"
			}
		}

		if source == "客户端" {
			source = "Agent"
		}

		match := false
		if sourceType == "ALL" || sourceType == "" {
			match = true
		} else if sourceType == "CLI" && source == "CLI" {
			match = true
		} else if sourceType == "IDE" && source == "IDE" {
			match = true
		} else if sourceType == "Agent" && source == "Agent" {
			match = true
		} else if sourceType == "UNKNOWN" && (source == "未知" || source == "") {
			match = true
		}

		if match {
			targets = append(targets, p)
		}
	}
	pc.RUnlock()

	if len(targets) == 0 {
		return "", fmt.Errorf("当前筛选的 [%s] 类型接口抓包日志为空，请先发起一些 API 请求！", sourceType)
	}

	if pc.getAccountTokens == nil {
		return "", errors.New("账号管理器未就绪")
	}

	accessToken, refreshToken, projectId, err := pc.getAccountTokens(accountId)
	if err != nil {
		return "", err
	}

	if accessToken == "" {
		return "", errors.New("该账号暂无有效的 Access Token")
	}

	executeRequest := func(token string) (int, []byte, error) {
		prompt := pc.generatePrompt(targets)
		if projectId == "" {
			projectId = "favorable-synapse-ttvcb"
		}

		reqBodyMap := map[string]interface{}{
			"project":   projectId,
			"requestId": fmt.Sprintf("chat/%d-%d", time.Now().Unix(), rand.Intn(1000000)),
			"request": map[string]interface{}{
				"contents": []interface{}{
					map[string]interface{}{
						"role": "user",
						"parts": []interface{}{
							map[string]interface{}{
								"text": prompt,
							},
						},
					},
				},
				"generationConfig": map[string]interface{}{
					"maxOutputTokens": 8192,
					"thinkingConfig": map[string]interface{}{
						"includeThoughts": false,
						"thinkingBudget":  0,
					},
				},
				"sessionId": fmt.Sprintf("-%d", time.Now().UnixNano()/1e6),
			},
			"model":              "gemini-2.5-flash-lite",
			"userAgent":          "antigravity",
			"requestType":        "chat",
			"enabledCreditTypes": []string{"GOOGLE_ONE_AI"},
		}

		jsonBody, err := json.Marshal(reqBodyMap)
		if err != nil {
			return 0, nil, err
		}

		req, err := http.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", bytes.NewBuffer(jsonBody))
		if err != nil {
			return 0, nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "antigravity/ide/2.8.4 windows/amd64")
		req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")

		client := netutil.NewClient(120 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		return resp.StatusCode, respBody, nil
	}

	statusCode, bodyBytes, err := executeRequest(accessToken)
	if err != nil {
		return "", err
	}

	if statusCode == 401 && refreshToken != "" && pc.refreshAccount != nil {
		fmt.Printf("[PacketCapturer] Token expired for account %s, refreshing...\n", accountId)
		newToken, refreshErr := pc.refreshAccount(accountId)
		if refreshErr == nil {
			statusCode, bodyBytes, err = executeRequest(newToken)
		} else {
			return "", fmt.Errorf("账号 Token 过期且自动刷新失败: %v", refreshErr)
		}
	}

	if err != nil {
		return "", err
	}

	if statusCode != 200 {
		var errorMsg = fmt.Sprintf("HTTP Error %d", statusCode)
		var errJson struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errJson) == nil && errJson.Error.Message != "" {
			errorMsg = errJson.Error.Message
		}
		return "", fmt.Errorf("Gemini 分析接口返回错误: %s", errorMsg)
	}

	// Parse SSE format
	bodyStr := strings.TrimSpace(string(bodyBytes))
	if strings.HasPrefix(bodyStr, "data:") {
		var fullText strings.Builder
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			cleanLine := strings.TrimSpace(line)
			if strings.HasPrefix(cleanLine, "data:") {
				jsonStr := strings.TrimSpace(cleanLine[5:])
				var data map[string]interface{}
				if json.Unmarshal([]byte(jsonStr), &data) == nil {
					// Check response candidates
					var resObj interface{}
					if val, ok := data["response"]; ok {
						resObj = val
					} else {
						resObj = data
					}

					if resMap, ok := resObj.(map[string]interface{}); ok {
						if candidates, ok := resMap["candidates"].([]interface{}); ok && len(candidates) > 0 {
							if candidateMap, ok := candidates[0].(map[string]interface{}); ok {
								if content, ok := candidateMap["content"].(map[string]interface{}); ok {
									if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
										if partMap, ok := parts[0].(map[string]interface{}); ok {
											if text, ok := partMap["text"].(string); ok {
												fullText.WriteString(text)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
		if fullText.Len() == 0 {
			return "", errors.New("SSE 响应中未包含任何文本内容")
		}
		return fullText.String(), nil
	}

	// Normal JSON response
	var respJson map[string]interface{}
	if json.Unmarshal(bodyBytes, &respJson) == nil {
		var resObj interface{}
		if val, ok := respJson["response"]; ok {
			resObj = val
		} else {
			resObj = respJson
		}

		if resMap, ok := resObj.(map[string]interface{}); ok {
			if candidates, ok := resMap["candidates"].([]interface{}); ok && len(candidates) > 0 {
				if candidateMap, ok := candidates[0].(map[string]interface{}); ok {
					if content, ok := candidateMap["content"].(map[string]interface{}); ok {
						if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
							if partMap, ok := parts[0].(map[string]interface{}); ok {
								if text, ok := partMap["text"].(string); ok {
									return text, nil
								}
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("解析 Gemini 响应数据失败，原始响应前300字符: %s", string(bodyBytes[:300]))
}

func (pc *PacketCapturer) generatePrompt(targets []*CapturedPacket) string {
	var prompt strings.Builder
	prompt.WriteString(`你是一个最顶级的 API 架构师和技术文档工程师。
下面是我在本地抓包拦截到的通过我们代理的真实 API 请求 and 响应日志。
请你以最严谨、详实、清晰的专业态度，分析这些抓包数据，提取并归纳出所有不同的 API 接口，并输出一份完整、美观的 Markdown 格式 of 接口文档说明。

> [!IMPORTANT]
> **请务必严格遵守以下“全面、明明白白”的文档编写要求，绝对不能漏掉任何字段，不能含糊带过：**
> 1. **请求 Header 必须表格化逐一解释**：将抓包中的每一个请求头字段（如 Host, User-Agent, Content-Type, Authorization 等）在表格中列出，详细说明其在接口中的作用和取值（对于 Authorization 或是 API Key 等敏感信息，请解释其作为鉴权凭据的作用）。
> 2. **请求 Body 字段深度拆解**：如果请求体是 JSON 格式，必须将 **每一个** 字段路径（包括所有嵌套的父级对象、数组以及最深层的叶子字段，必须使用 'a.b[].c' 的形式）都在表格中逐一列出！表格必须包含：字段路径、字段名称（或简短 key 名字）、数据类型、是否必填（结合实际推断）、中文说明（结合真实业务场景，解释得清清楚楚、明明白白，说明它对模型生成或控制的具体作用）、示例值。
> 3. **响应 Body 字段深度拆解**：对于返回的 JSON 结构，同样必须在表格中将 **每一个** 响应字段路径全量列出并逐个解释，详细说清各个字段的业务含义、作用以及在开发中如何使用。
> 4. **字段实际具体值/枚举值的深度业务逆向解读**：在解释字段时，**必须结合抓到的真实具体值**（例如在响应中出现的 creditType: "GOOGLE_ONE_AI"、paidTier.id: "g1-pro-tier"、minimumCreditAmountForUsage: "50" 等点）在说明表格中做详尽的业务逻辑剖析。例如解释 GOOGLE_ONE_AI 代表什么类型的订阅积分、g1-pro-tier 的会员等级权限、50 的使用门槛限制以及其具体的业务逻辑，不允许只做英文字面直译。
> 5. **宁多勿漏，对照必填字段清单**：我们为每个接口自动提取了所有的 Body 字段路径清单。你所输出的参数说明表格中，**必须全量包含**清单中的每一个路径！如果遗漏任何一个，接口文档将视为不合格。绝对不允许使用“等等/其余字段略”等借口省略任何字段！
> 6. **请求与响应示例代码块绝对不能丢失**：对于每一个接口，必须在其定义的最末尾提供 '请求与响应示例' 章节，并将下方提供给你的对应请求 Body JSON 与响应 Body JSON 完整输出（使用 json 语法高亮代码块包裹），这对于调用者极其关键，绝对不可以省略！

请按照以下结构组织 Markdown 文档：
1. **接口文档整体概览**：表格展示所有 API 列表（序号、方法、路径、接口名称说明）。
2. **详细接口定义**（每个接口用独立标题拆分）：
   - **接口中文名称**（根据 Path 和业务内容推断合理好懂的名称）
   - **请求方法 (Method)** 与 **请求完整 URL 路径**
   - **请求 Headers 说明**（详细表格：字段、说明、示例）
   - **请求 Body 参数说明**（超级详细的表格：字段路径、字段名称、类型、必填、详细含义说明、示例。必须包含我们为你列出的所有请求字段路径）
   - **响应 Body 参数说明**（超级详细的表格：字段路径、字段名称、类型、详细含义说明、示例。必须包含我们为你列出的所有响应字段路径）
   - **请求与响应示例**：必须完整输出该接口的请求 JSON 与响应 JSON 代码块。你必须直接使用我们在下方提供的已经过智能压缩的 '请求Body 示例' 与 '响应Body 示例' 填充，严禁缩减、省略或用“略”字代替，确保代码块包含在生成的 Markdown 中！

下面是抓包得到的真实接口日志（共 `)
	prompt.WriteString(fmt.Sprintf("%d", len(targets)))
	prompt.WriteString(" 个）：\n\n")

	for idx, p := range targets {
		truncatedReq := smartTruncateJson(p.ReqBody, 120)
		truncatedRes := smartTruncateJson(p.ResBody, 120)

		reqBodyStr := "{}"
		if truncatedReq != nil {
			if b, err := json.MarshalIndent(truncatedReq, "", "  "); err == nil {
				reqBodyStr = string(b)
			}
		}
		resBodyStr := "{}"
		if truncatedRes != nil {
			if b, err := json.MarshalIndent(truncatedRes, "", "  "); err == nil {
				resBodyStr = string(b)
			}
		}

		reqPaths := extractFieldPaths(p.ReqBody, "")
		resPaths := extractFieldPaths(p.ResBody, "")

		var reqPathsMD strings.Builder
		if len(reqPaths) > 0 {
			for _, path := range reqPaths {
				reqPathsMD.WriteString("- `" + path + "`\n")
			}
		} else {
			reqPathsMD.WriteString("无字段\n")
		}

		var resPathsMD strings.Builder
		if len(resPaths) > 0 {
			for _, path := range resPaths {
				resPathsMD.WriteString("- `" + path + "`\n")
			}
		} else {
			resPathsMD.WriteString("无字段\n")
		}

		reqHBytes, _ := json.MarshalIndent(p.ReqHeaders, "", "  ")
		resHBytes, _ := json.MarshalIndent(p.ResHeaders, "", "  ")

		prompt.WriteString(fmt.Sprintf(`---
[接口 #%d]
Method: %s
URL: %s
请求Headers: %s

【此接口必须解释的请求 Body 字段路径清单（共 %d 个，表格中必须全部包含解释）】：
%s
请求Body 示例（已智能折叠超长数据）:
`+"```json\n%s\n```"+`

【此接口必须解释的响应 Body 字段路径清单（共 %d 个，表格中必须全部包含解释）】：
%s
响应Body 示例（已智能折叠超长数据）:
`+"```json\n%s\n```"+`

响应Headers: %s
`, idx+1, p.Method, p.URL, string(reqHBytes), len(reqPaths), reqPathsMD.String(), reqBodyStr, len(resPaths), resPathsMD.String(), resBodyStr, string(resHBytes)))
	}

	prompt.WriteString("\n直接输出最详实的 Markdown 内容，不要有任何客套废话或解释性前言，直接以漂亮的 Markdown 格式输出。")
	return prompt.String()
}
