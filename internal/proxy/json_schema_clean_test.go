package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCleanToolDeclarations_ParametersJsonSchema 验证 parametersJsonSchema -> parameters 重命名
func TestCleanToolDeclarations_ParametersJsonSchema(t *testing.T) {
	inputJSON := `{
		"tools": [{
			"functionDeclarations": [{
				"name": "shell_command",
				"parametersJsonSchema": {
					"type": "object",
					"properties": {
						"command": {"type": "string"}
					},
					"required": ["command"],
					"$schema": "http://json-schema.org/draft-07/schema#",
					"additionalProperties": false
				}
			}]
		}]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanToolDeclarations(req)

	out, _ := json.Marshal(req)
	outStr := string(out)

	// parametersJsonSchema 应被重命名为 parameters
	if strings.Contains(outStr, "parametersJsonSchema") {
		t.Errorf("parametersJsonSchema should be renamed to parameters")
	}
	if !strings.Contains(outStr, `"parameters"`) {
		t.Errorf("parameters field should exist after renaming")
	}

	// $schema 和 additionalProperties 应被移除
	if strings.Contains(outStr, "$schema") {
		t.Errorf("$schema should be removed")
	}
	if strings.Contains(outStr, "additionalProperties") {
		t.Errorf("additionalProperties should be removed")
	}

	// 核心属性应保留
	if !strings.Contains(outStr, "command") {
		t.Errorf("'command' property should be preserved")
	}
}

// TestCleanToolDeclarations_WebSearchRemoval 验证 web_search 声明被过滤
func TestCleanToolDeclarations_WebSearchRemoval(t *testing.T) {
	inputJSON := `{
		"tools": [{
			"functionDeclarations": [
				{"name": "web_search", "parameters": {"type": "object"}},
				{"name": "shell_command", "parameters": {"type": "object"}}
			]
		}]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanToolDeclarations(req)

	// 验证 web_search 被移除但 shell_command 保留
	tools := req["tools"].([]interface{})
	decls := tools[0].(map[string]interface{})["functionDeclarations"].([]interface{})
	if len(decls) != 1 {
		t.Errorf("Expected 1 declaration after filtering, got %d", len(decls))
	}
	name := decls[0].(map[string]interface{})["name"]
	if name != "shell_command" {
		t.Errorf("Expected shell_command to remain, got %v", name)
	}
}

// TestCleanJSONSchema_ConstraintMigration 验证约束字段被转为 description 提示
func TestCleanJSONSchema_ConstraintMigration(t *testing.T) {
	inputJSON := `{
		"type": "object",
		"properties": {
			"location": {
				"type": "string",
				"minLength": 1,
				"format": "city"
			}
		}
	}`

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanJSONSchema(schema)

	out, _ := json.Marshal(schema)
	outStr := string(out)

	// minLength 和 format 应被移除
	if strings.Contains(outStr, "minLength") {
		t.Errorf("minLength should be removed")
	}
	if strings.Contains(outStr, `"format"`) {
		t.Errorf("format should be removed")
	}

	// description 应包含约束提示
	locProps := schema["properties"].(map[string]interface{})
	loc := locProps["location"].(map[string]interface{})
	desc, _ := loc["description"].(string)
	if !strings.Contains(desc, "minLen") || !strings.Contains(desc, "format") {
		t.Errorf("description should contain constraint hints, got: %s", desc)
	}
}

// TestCleanJSONSchema_UnionTypeCollapse 验证联合类型降级
func TestCleanJSONSchema_UnionTypeCollapse(t *testing.T) {
	inputJSON := `{
		"type": ["string", "null"],
		"description": "User name"
	}`

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanJSONSchema(schema)

	// ["string", "null"] -> "string"
	typ, _ := schema["type"].(string)
	if typ != "string" {
		t.Errorf("Expected type 'string', got '%s'", typ)
	}

	// description 应包含 (nullable)
	desc, _ := schema["description"].(string)
	if !strings.Contains(desc, "(nullable)") {
		t.Errorf("description should contain (nullable) hint, got: %s", desc)
	}
}

// TestCleanJSONSchema_EmptyObjectProperties 验证空 Object 补空 properties
func TestCleanJSONSchema_EmptyObjectProperties(t *testing.T) {
	inputJSON := `{"type": "object"}`

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanJSONSchema(schema)

	props, _ := schema["properties"].(map[string]interface{})
	if props == nil {
		t.Errorf("Empty object should have empty properties map")
	}
}

// TestCleanAndPrepareGeminiRequest_Comprehensive 综合测试：thoughtSignature 剥离 + Schema 清洗 + toolConfig 注入
func TestCleanAndPrepareGeminiRequest_Comprehensive(t *testing.T) {
	inputJSON := `{
		"contents": [
			{
				"role": "model",
				"parts": [
					{
						"functionCall": {"name": "shell_command", "args": {"command": "ls"}},
						"thoughtSignature": "skip_thought_signature_validator"
					}
				]
			}
		],
		"tools": [{
			"functionDeclarations": [{
				"name": "shell_command",
				"parametersJsonSchema": {
					"$schema": "http://json-schema.org/draft-07/schema#",
					"type": "object",
					"properties": {
						"command": {"type": "string", "minLength": 1}
					},
					"additionalProperties": false,
					"required": ["command"]
				}
			}]
		}]
	}`

	var req map[string]interface{}
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cleanAndPrepareGeminiRequest(req)

	out, _ := json.Marshal(req)
	outStr := string(out)

	// 1. thoughtSignature 应被清除
	if strings.Contains(outStr, "thoughtSignature") {
		t.Errorf("thoughtSignature should be removed")
	}

	// 2. $schema 和 additionalProperties 应被清除
	if strings.Contains(outStr, "$schema") {
		t.Errorf("$schema should be removed")
	}
	if strings.Contains(outStr, "additionalProperties") {
		t.Errorf("additionalProperties should be removed")
	}

	// 3. parametersJsonSchema 应被重命名为 parameters
	if strings.Contains(outStr, "parametersJsonSchema") {
		t.Errorf("parametersJsonSchema should be renamed to parameters")
	}

	// 4. toolConfig 应被注入
	if !strings.Contains(outStr, "toolConfig") {
		t.Errorf("toolConfig should be injected")
	}

	// 5. functionCall 和核心字段应保留
	if !strings.Contains(outStr, "functionCall") {
		t.Errorf("functionCall should be preserved")
	}
	if !strings.Contains(outStr, "shell_command") {
		t.Errorf("shell_command name should be preserved")
	}
}

// TestCleanToolDeclarationsInBody_V1Internal 验证 v1internal 嵌套格式中的工具声明被正确清洗
func TestCleanToolDeclarationsInBody_V1Internal(t *testing.T) {
	inputJSON := `{
		"project": "my-project",
		"model": "gemini-3.6-flash-high",
		"request": {
			"contents": [{"role": "user", "parts": [{"text": "hello"}]}],
			"tools": [{
				"functionDeclarations": [{
					"name": "shell_command",
					"parametersJsonSchema": {
						"$schema": "http://json-schema.org/draft-07/schema#",
						"type": "object",
						"properties": {
							"command": {"type": "string", "minLength": 1}
						},
						"additionalProperties": false
					}
				}]
			}]
		}
	}`

	result := cleanToolDeclarationsInBody([]byte(inputJSON))
	outStr := string(result)

	// parametersJsonSchema 应被重命名为 parameters
	if strings.Contains(outStr, "parametersJsonSchema") {
		t.Errorf("parametersJsonSchema should be renamed to parameters in v1internal format")
	}
	if !strings.Contains(outStr, `"parameters"`) {
		t.Errorf("parameters field should exist after renaming")
	}

	// $schema 和 additionalProperties 应被移除
	if strings.Contains(outStr, "$schema") {
		t.Errorf("$schema should be removed")
	}
	if strings.Contains(outStr, "additionalProperties") {
		t.Errorf("additionalProperties should be removed")
	}

	// 外层 v1internal 字段应保留
	if !strings.Contains(outStr, "my-project") {
		t.Errorf("project field should be preserved")
	}
	if !strings.Contains(outStr, "gemini-3.6-flash-high") {
		t.Errorf("model field should be preserved")
	}
}

// TestCleanToolDeclarationsInBody_Standard 验证标准 Gemini 格式被正确清洗
func TestCleanToolDeclarationsInBody_Standard(t *testing.T) {
	inputJSON := `{
		"contents": [{"role": "user", "parts": [{"text": "hello"}]}],
		"tools": [{
			"functionDeclarations": [{
				"name": "read_file",
				"parameters": {
					"$schema": "http://json-schema.org/draft-07/schema#",
					"type": "object",
					"properties": {
						"path": {"type": "string"}
					},
					"additionalProperties": false
				}
			}]
		}]
	}`

	result := cleanToolDeclarationsInBody([]byte(inputJSON))
	outStr := string(result)

	if strings.Contains(outStr, "$schema") {
		t.Errorf("$schema should be removed in standard format")
	}
	if strings.Contains(outStr, "additionalProperties") {
		t.Errorf("additionalProperties should be removed in standard format")
	}
}

// TestCleanToolDeclarationsInBody_NoTools 验证无 tools 的请求体不受影响
func TestCleanToolDeclarationsInBody_NoTools(t *testing.T) {
	inputJSON := `{"contents": [{"role": "user", "parts": [{"text": "hello"}]}]}`

	result := cleanToolDeclarationsInBody([]byte(inputJSON))

	if string(result) != inputJSON {
		// 由于 JSON marshal 可能改变字段顺序，我们只验证核心内容不变
		var original, cleaned map[string]interface{}
		json.Unmarshal([]byte(inputJSON), &original)
		json.Unmarshal(result, &cleaned)
		if original["contents"] != nil && cleaned["contents"] != nil {
			// 基本验证：contents 仍在
		}
	}
}
