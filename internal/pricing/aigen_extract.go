// Package pricing: aigen_extract.go —— AI 计费生成上游响应的 SSE/grounding/JSON 解析。
//
// 从 aigen.go 抽离的全是纯解析函数,无 AIPriceGenerator 依赖,便于单测与维护。
//
// 三处相对旧版的关键增强:
//  1. extractSSEText 改为同时返回文本与 grounding 来源引用(extractSSETextWithMeta)
//     旧版只取 candidates[0].content.parts[0].text,完全丢弃 groundingMetadata,
//     导致无法判断 AI 是否真联网搜了网——这是「假牌价」根因之一;
//  2. 新增 extractGroundingMetadata:从 candidates[0].groundingMetadata.groundingChunks[].web.{uri,title}
//     抽取引用来源,透出给前端行,让用户能点开核对 AI 引用的是哪页官方价;
//  3. firstCandidateText 修复只取 parts[0] 的漏读:grounding 启用时 parts 可能有
//     text / thoughtSummary / executableCode 多段,改为遍历所有 part 拼接 text。
//
// aiPriceEntry 结构同步扩展 Estimated/SourceURL 字段,支持 prompt 重写后的可选标记。
package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// WebRef 是 AI 联网检索引用的来源网页引用。
type WebRef struct {
	URI   string `json:"uri"`
	Title string `json:"title"`
}

// sseExtract 是 extractSSEText 的结构化返回:拼接后的纯文本 + 累积的 grounding 来源引用。
type sseExtract struct {
	Text    string
	Sources []WebRef
}

// extractSSEText 从 daily-cloudcode-pa 的 SSE 响应里抽取候选文本与 grounding 来源,
// 逻辑与 packet.go:617-688 的 SSE/JSON 双形态抽取逐字对齐(就地复制,避免 import stats):
//   - 优先按 "data:" 行解析,逐行取 response.candidates[0].content.parts[*].text 拼接,
//     并从 candidates[0].groundingMetadata.groundingChunks[].web 累积来源引用;
//   - 非 SSE 形态时按普通 JSON 解析同一候选 + grounding 路径。
//
// 旧版 extractSSEText 只返回 string,本版拆为 extractSSETextWithMeta(带来源),
// 并保留 extractSSEText 兼容签名供仅需文本的调用方(及测试)使用。
func extractSSETextWithMeta(bodyBytes []byte) (sseExtract, error) {
	bodyStr := strings.TrimSpace(string(bodyBytes))
	if strings.HasPrefix(bodyStr, "data:") {
		var fullText strings.Builder
		srcSeen := make(map[string]struct{})
		var sources []WebRef
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			cleanLine := strings.TrimSpace(line)
			if !strings.HasPrefix(cleanLine, "data:") {
				continue
			}
			jsonStr := strings.TrimSpace(cleanLine[5:])
			var data map[string]interface{}
			if json.Unmarshal([]byte(jsonStr), &data) != nil {
				continue
			}
			if t := firstCandidateText(data); t != "" {
				fullText.WriteString(t)
			}
			// 累积本帧的 grounding 来源,按 URI 去重避免重复刷屏。
			for _, src := range extractGroundingMetadata(data) {
				if src.URI == "" {
					continue
				}
				if _, dup := srcSeen[src.URI]; dup {
					continue
				}
				srcSeen[src.URI] = struct{}{}
				sources = append(sources, src)
			}
		}
		if fullText.Len() > 0 {
			return sseExtract{Text: fullText.String(), Sources: sources}, nil
		}
		return sseExtract{}, errors.New("AI 定价 SSE 响应中未包含任何文本内容")
	}

	// 普通 JSON 形态(非流式响应)。
	var respJson map[string]interface{}
	if json.Unmarshal(bodyBytes, &respJson) == nil {
		t := firstCandidateText(respJson)
		if t != "" {
			return sseExtract{Text: t, Sources: extractGroundingMetadata(respJson)}, nil
		}
	}
	return sseExtract{}, fmt.Errorf("解析 AI 定价响应失败,原始响应前300字符: %s", truncateBody(bodyBytes))
}

// extractSSEText 是 extractSSETextWithMeta 的兼容版,仅返回文本(丢弃来源),
// 保留供仅需文本的旧调用路径与测试使用。生产路径用 extractSSETextWithMeta。
func extractSSEText(bodyBytes []byte) (string, error) {
	r, err := extractSSETextWithMeta(bodyBytes)
	if err != nil {
		return "", err
	}
	return r.Text, nil
}

// firstCandidateText 从一个响应对象里抽取 candidates[0].content.parts[*].text 拼接,
// 兼容响应直接就是候选对象或被包在 "response" 键下两种形态(与 packet.go 同款)。
//
// 相对旧版修复:不再只取 parts[0],而是遍历所有 part 把 text 段拼起来——
// grounding 启用时 parts 可能有 text / thoughtSummary / executableCode 等多段,
// 只取 parts[0] 会漏掉后续 text 片段。
func firstCandidateText(data map[string]interface{}) string {
	var resObj interface{}
	if val, ok := data["response"]; ok {
		resObj = val
	} else {
		resObj = data
	}
	resMap, ok := resObj.(map[string]interface{})
	if !ok {
		return ""
	}
	candidates, ok := resMap["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return ""
	}
	candidateMap, ok := candidates[0].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := candidateMap["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	parts, ok := content["parts"].([]interface{})
	if !ok || len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		partMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if text, ok := partMap["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}

// extractGroundingMetadata 从一个响应对象里抽取 candidates[0].groundingMetadata 的来源引用。
//
// Gemini 联网检索(grounding)响应里,引用来源放在:
//   - candidates[0].groundingMetadata.groundingChunks[].web.{uri,title}
//   - candidates[0].groundingMetadata.searchEntryPoint(执行了的检索入口点描述)
//   - candidates[0].groundingMetadata.webSearchQueries(实际下发的检索 queries)
//
// 本函数只抽取可展示给用户的来源引用(groundingChunks[].web 的 uri+title),
// searchEntryPoint/webSearchQueries 对前端用户校验帮助有限,暂不透出。
// 兼容响应直接是候选对象或被包在 "response" 键下两种形态。
func extractGroundingMetadata(data map[string]interface{}) []WebRef {
	var resObj interface{}
	if val, ok := data["response"]; ok {
		resObj = val
	} else {
		resObj = data
	}
	resMap, ok := resObj.(map[string]interface{})
	if !ok {
		return nil
	}
	candidates, ok := resMap["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return nil
	}
	candidateMap, ok := candidates[0].(map[string]interface{})
	if !ok {
		return nil
	}
	gm, ok := candidateMap["groundingMetadata"].(map[string]interface{})
	if !ok {
		return nil
	}
	chunks, ok := gm["groundingChunks"].([]interface{})
	if !ok || len(chunks) == 0 {
		return nil
	}
	sources := make([]WebRef, 0, len(chunks))
	for _, c := range chunks {
		chunk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		web, ok := chunk["web"].(map[string]interface{})
		if !ok {
			continue
		}
		uri, _ := web["uri"].(string)
		title, _ := web["title"].(string)
		if uri == "" && title == "" {
			continue
		}
		sources = append(sources, WebRef{URI: uri, Title: title})
	}
	return sources
}

// aiPriceEntry 是 AI 返回的定价数组单元素结构(prompt 重写后支持可选 estimated/source_url 字段)。
type aiPriceEntry struct {
	Name      string  `json:"name"`
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	Cached    float64 `json:"cached"`
	Estimated bool    `json:"estimated,omitempty"`
	SourceURL string  `json:"source_url,omitempty"`
}

// aiPriceParsed 是 extractPricingJSON 的内部解析结果,承载 Rate + 估算标记 + 引用 URL。
type aiPriceParsed struct {
	Rate      ModelRate
	Estimated bool
	SourceURL string
}

// extractPricingJSON 从 AI 返回的纯文本里取第一个 '[' 到最后一个 ']' 的子串,
// json.Unmarshal 到 []aiPriceEntry,再以 "name 低小写" → aiPriceParsed 的 map 返回。
// 兼容裸数组、```json 代码块包裹、带前导解释文本三种形态。
// 解析失败或提取不到数组时返回空 map(Generate 会给每个输入模型补零)。
func extractPricingJSON(text string) map[string]aiPriceParsed {
	result := map[string]aiPriceParsed{}
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end < 0 || end <= start {
		return result
	}
	jsonStr := text[start : end+1]
	var entries []aiPriceEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return result
	}
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		result[strings.ToLower(strings.TrimSpace(e.Name))] = aiPriceParsed{
			Rate: ModelRate{
				Input:  e.Input,
				Output: e.Output,
				Cached: e.Cached,
			},
			Estimated: e.Estimated,
			SourceURL: strings.TrimSpace(e.SourceURL),
		}
	}
	return result
}
