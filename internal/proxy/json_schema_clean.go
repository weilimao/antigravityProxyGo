package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cleanToolDeclarations 清洗请求体中的工具声明，使其符合 Gemini API 要求。
// 这是防止 MALFORMED_FUNCTION_CALL 的核心手段：
// Gemini 不支持 JSON Schema 中的许多标准字段（如 $schema, additionalProperties, format, default 等），
// 保留这些字段会导致模型生成格式错误的函数调用，触发 MALFORMED_FUNCTION_CALL 终止流。
//
// 参考 Antigravity-Manager 的 clean_json_schema 实现。
func cleanToolDeclarations(req map[string]interface{}) {
	toolsVal, hasTools := req["tools"]
	if !hasTools {
		return
	}
	toolsArr, ok := toolsVal.([]interface{})
	if !ok {
		return
	}

	for _, toolVal := range toolsArr {
		tool, ok := toolVal.(map[string]interface{})
		if !ok {
			continue
		}

		declsVal, hasDecls := tool["functionDeclarations"]
		if !hasDecls {
			continue
		}
		declsArr, ok := declsVal.([]interface{})
		if !ok {
			continue
		}

		// 过滤掉 web_search / google_search 声明（与内置 googleSearch 冲突）
		var filteredDecls []interface{}
		for _, declVal := range declsArr {
			decl, ok := declVal.(map[string]interface{})
			if !ok {
				filteredDecls = append(filteredDecls, declVal)
				continue
			}
			name, _ := decl["name"].(string)
			if name == "web_search" || name == "google_search" {
				continue
			}

			// 处理 parametersJsonSchema -> parameters 重命名
			if pjs, hasPJS := decl["parametersJsonSchema"]; hasPJS {
				delete(decl, "parametersJsonSchema")
				params, ok := pjs.(map[string]interface{})
				if ok {
					cleanJSONSchema(params)
				}
				decl["parameters"] = pjs
			} else if params, hasParams := decl["parameters"]; hasParams {
				// 标准参数字段，也需要清洗
				if paramsMap, ok := params.(map[string]interface{}); ok {
					cleanJSONSchema(paramsMap)
				}
			}

			filteredDecls = append(filteredDecls, decl)
		}
		tool["functionDeclarations"] = filteredDecls
	}
}

// cleanJSONSchema 递归清理 JSON Schema 以符合 Gemini 接口要求。
// 移除 Gemini 不支持的字段，将约束信息转换为 description 提示。
//
// 核心规则：
//  1. 移除 $schema, additionalProperties, default, uniqueItems
//  2. 将约束字段（minLength, maxLength, pattern, minimum, maximum 等）转为 description 提示后移除
//  3. 处理联合类型 ["string", "null"] -> "string"
//  4. 处理 anyOf/oneOf：选择最佳非 null 分支
//  5. 将 type 字段的值转为小写（Gemini 要求）
//  6. 空 Object 补空 properties
//  7. $ref 展平（Gemini 不支持 $ref）
func cleanJSONSchema(schema map[string]interface{}) {
	cleanJSONSchemaRecursive(schema, true, 0)
}

const maxSchemaDepth = 10

// 约束字段：在被白名单过滤前，将校验项转为 description Hint
var constraintFields = []struct {
	field string
	label string
}{
	{"minLength", "minLen"},
	{"maxLength", "maxLen"},
	{"pattern", "pattern"},
	{"minimum", "min"},
	{"maximum", "max"},
	{"multipleOf", "multipleOf"},
	{"exclusiveMinimum", "exclMin"},
	{"exclusiveMaximum", "exclMax"},
	{"minItems", "minItems"},
	{"maxItems", "maxItems"},
	{"format", "format"},
}

// Gemini 白名单：只保留这些字段
var schemaAllowedFields = map[string]bool{
	"type":        true,
	"description": true,
	"properties":  true,
	"required":    true,
	"items":       true,
	"enum":        true,
	"title":       true,
}

func cleanJSONSchemaRecursive(value map[string]interface{}, isSchemaNode bool, depth int) {
	if depth > maxSchemaDepth {
		return
	}

	// 0. 处理 $ref 展开（简化版：移除 $ref，降级为 string）
	if refPath, hasRef := value["$ref"]; hasRef {
		delete(value, "$ref")
		if _, hasType := value["type"]; !hasType {
			value["type"] = "string"
		}
		refStr, _ := refPath.(string)
		refName := refStr
		if idx := strings.LastIndex(refStr, "/"); idx >= 0 {
			refName = refStr[idx+1:]
		}
		appendDescriptionHint(value, fmt.Sprintf("(Unresolved $ref: %s)", refName))
	}

	// 移除 $defs / definitions
	delete(value, "$defs")
	delete(value, "definitions")
	delete(value, "$schema")

	// 1. 递归处理 properties
	if props, ok := value["properties"].(map[string]interface{}); ok {
		for k, v := range props {
			// 移除 boolean sub-schemas（Gemini 不支持）
			if _, isBool := v.(bool); isBool {
				delete(props, k)
				continue
			}
			if propMap, ok := v.(map[string]interface{}); ok {
				cleanJSONSchemaRecursive(propMap, true, depth+1)
			}
		}

		// 移除 required 中已删除的属性
		if reqArr, ok := value["required"].([]interface{}); ok {
			var filtered []interface{}
			for _, r := range reqArr {
				if rStr, ok := r.(string); ok {
					if _, exists := props[rStr]; exists {
						filtered = append(filtered, r)
					}
				}
			}
			if len(filtered) > 0 {
				value["required"] = filtered
			} else {
				delete(value, "required")
			}
		}

		// 隐式类型注入：有 properties 但没 type，补全为 object
		if _, hasType := value["type"]; !hasType {
			value["type"] = "object"
		}
	}

	// 2. 递归处理 items
	if items, ok := value["items"].(map[string]interface{}); ok {
		cleanJSONSchemaRecursive(items, true, depth+1)
		// 隐式类型注入
		if _, hasType := value["type"]; !hasType {
			value["type"] = "array"
		}
	}

	// 3. 处理 anyOf/oneOf：选择最佳非 null 分支
	unionArray := resolveUnionType(value)
	if unionArray != nil {
		bestBranch := extractBestSchemaFromUnion(unionArray)
		if bestBranch != nil {
			// 合并最佳分支的属性到当前 map
			for k, v := range bestBranch {
				if k == "properties" {
					if targetProps, ok := value["properties"].(map[string]interface{}); ok {
						if srcProps, ok := v.(map[string]interface{}); ok {
							for pk, pv := range srcProps {
								if _, exists := targetProps[pk]; !exists {
									targetProps[pk] = pv
								}
							}
						}
					} else {
						value["properties"] = v
					}
				} else if k == "required" {
					if targetReq, ok := value["required"].([]interface{}); ok {
						if srcReq, ok := v.([]interface{}); ok {
							for _, rv := range srcReq {
								found := false
								for _, existing := range targetReq {
									if existing == rv {
										found = true
										break
									}
								}
								if !found {
									targetReq = append(targetReq, rv)
								}
							}
							value["required"] = targetReq
						}
					} else {
						value["required"] = v
					}
				} else if _, exists := value[k]; !exists {
					value[k] = v
				}
			}
		}
	}

	// 4. 判断是否为 Schema 节点
	// 不对 functionCall / functionResponse 等非 Schema 对象执行白名单过滤
	isNotSchemaPayload := value["functionCall"] != nil || value["functionResponse"] != nil
	hasStandardKeyword := false
	for k := range schemaAllowedFields {
		if value[k] != nil {
			hasStandardKeyword = true
			break
		}
	}
	looksLikeSchema := (isSchemaNode || hasStandardKeyword) && !isNotSchemaPayload

	if looksLikeSchema {
		// 5. 约束迁移：在被白名单过滤前，将校验项转为 description Hint
		moveConstraintsToDescription(value)

		// 6. 白名单过滤：移除 Gemini 不支持的字段
		for k := range value {
			if !schemaAllowedFields[k] {
				delete(value, k)
			}
		}

		// 7. 空 Object 补空 properties
		if typ, _ := value["type"].(string); typ == "object" {
			if _, hasProps := value["properties"]; !hasProps {
				value["properties"] = map[string]interface{}{}
			}
		}

		// 8. Required 字段对齐：移除不在 properties 中的 required 项
		if props, ok := value["properties"].(map[string]interface{}); ok {
			if reqArr, ok := value["required"].([]interface{}); ok {
				var filtered []interface{}
				for _, r := range reqArr {
					if rStr, ok := r.(string); ok {
						if _, exists := props[rStr]; exists {
							filtered = append(filtered, r)
						}
					}
				}
				if len(filtered) > 0 {
					value["required"] = filtered
				} else {
					delete(value, "required")
				}
			}
		}

		// 9. 补全缺失的 type
		if _, hasType := value["type"]; !hasType {
			if _, hasEnum := value["enum"]; hasEnum {
				value["type"] = "string"
			} else if _, hasProps := value["properties"]; hasProps {
				value["type"] = "object"
			} else if _, hasItems := value["items"]; hasItems {
				value["type"] = "array"
			}
		}

		// 10. 处理 type 字段：联合类型降级 + 小写化
		if typeVal, ok := value["type"]; ok {
			switch t := typeVal.(type) {
			case string:
				lower := strings.ToLower(t)
				if lower == "null" {
					delete(value, "type")
					appendDescriptionHint(value, "(nullable)")
				} else {
					value["type"] = lower
				}
			case []interface{}:
				// ["string", "null"] -> "string"
				selectedType := ""
				for _, item := range t {
					if s, ok := item.(string); ok {
						lower := strings.ToLower(s)
						if lower == "null" {
							appendDescriptionHint(value, "(nullable)")
						} else if selectedType == "" {
							selectedType = lower
						}
					}
				}
				if selectedType != "" {
					value["type"] = selectedType
				}
			}
		}

		// 11. Enum 值强制转字符串
		if enumArr, ok := value["enum"].([]interface{}); ok {
			for i, item := range enumArr {
				if _, isStr := item.(string); !isStr {
					enumArr[i] = fmt.Sprintf("%v", item)
				}
			}
		}
	}
}

// resolveUnionType 提取 anyOf 或 oneOf 数组
func resolveUnionType(value map[string]interface{}) []interface{} {
	if anyOf, ok := value["anyOf"].([]interface{}); ok {
		return anyOf
	}
	if oneOf, ok := value["oneOf"].([]interface{}); ok {
		return oneOf
	}
	return nil
}

// extractBestSchemaFromUnion 从 anyOf/oneOf 联合类型数组中选取最佳非 null Schema 分支
func extractBestSchemaFromUnion(unionArray []interface{}) map[string]interface{} {
	var bestBranch map[string]interface{}
	bestScore := -1

	for _, item := range unionArray {
		branch, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		score := scoreSchemaOption(branch)
		if score > bestScore {
			bestScore = score
			bestBranch = branch
		}
	}
	return bestBranch
}

// scoreSchemaOption 计算 Schema 分支的复杂度得分
// Object (3) > Array (2) > Scalar (1) > Null (0)
func scoreSchemaOption(branch map[string]interface{}) int {
	if _, hasProps := branch["properties"]; hasProps {
		return 3
	}
	if typ, _ := branch["type"].(string); strings.ToLower(typ) == "object" {
		return 3
	}
	if _, hasItems := branch["items"]; hasItems {
		return 2
	}
	if typ, _ := branch["type"].(string); strings.ToLower(typ) == "array" {
		return 2
	}
	if typ, _ := branch["type"].(string); strings.ToLower(typ) != "null" && typ != "" {
		return 1
	}
	return 0
}

// moveConstraintsToDescription 将约束字段转化为 description 提示
func moveConstraintsToDescription(schema map[string]interface{}) {
	var hints []string
	for _, cf := range constraintFields {
		if val, exists := schema[cf.field]; exists && val != nil {
			valStr := fmt.Sprintf("%v", val)
			hints = append(hints, fmt.Sprintf("%s: %s", cf.label, valStr))
		}
	}
	if len(hints) > 0 {
		appendDescriptionHint(schema, fmt.Sprintf("[Constraint: %s]", strings.Join(hints, ", ")))
	}
}

// appendDescriptionHint 追加提示信息到 description 字段
func appendDescriptionHint(schema map[string]interface{}, hint string) {
	desc, _ := schema["description"].(string)
	if desc == "" {
		schema["description"] = hint
	} else if !strings.Contains(desc, hint) {
		schema["description"] = desc + " " + hint
	}
}

// cleanAndPrepareGeminiRequest 对降级翻译后的请求体执行完整的 Gemini 兼容性清洗：
//  1. 清除 thoughtSignature（标准 API 不支持）
//  2. 清洗工具声明中的 JSON Schema（防止 MALFORMED_FUNCTION_CALL）
//  3. 注入 toolConfig（宽松模式）
func cleanAndPrepareGeminiRequest(req map[string]interface{}) {
	// 递归清除 thoughtSignature
	stripThoughtSignature(req)

	// 清洗工具声明
	cleanToolDeclarations(req)

	// 注入 toolConfig
	if _, hasToolConfig := req["toolConfig"]; !hasToolConfig {
		if _, hasTools := req["tools"]; hasTools {
			req["toolConfig"] = map[string]interface{}{
				"functionCallingConfig": map[string]interface{}{
					"mode": "AUTO",
				},
			}
		}
	}
}

// cleanToolDeclarationsInBody 对原始 JSON 请求体执行工具声明清洗。
// 支持标准 Gemini 格式和 v1internal 嵌套格式，自动检测并处理。
// 这是全局入口，在请求到达上游前统一清洗，无论走 v1internal 还是降级路径。
func cleanToolDeclarationsInBody(bodyBytes []byte) []byte {
	if len(bodyBytes) == 0 {
		return bodyBytes
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &doc); err != nil {
		return bodyBytes // 非 JSON 或解析失败，原样返回
	}

	changed := false

	// 情况1：v1internal 嵌套格式 — 清洗 request 内部的 tools
	if reqObj, ok := doc["request"].(map[string]interface{}); ok {
		if _, hasTools := reqObj["tools"]; hasTools {
			cleanToolDeclarations(reqObj)
			changed = true
		}
	}

	// 情况2：标准 Gemini 格式 — 直接清洗顶层的 tools
	if _, hasTools := doc["tools"]; hasTools {
		cleanToolDeclarations(doc)
		changed = true
	}

	if !changed {
		return bodyBytes
	}

	newBytes, err := json.Marshal(doc)
	if err != nil {
		return bodyBytes // 序列化失败，保留原始请求体
	}
	return newBytes
}
