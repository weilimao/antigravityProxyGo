package main

import (
	"encoding/json"
	"strings"

	"antigravity-proxy/internal/account"
)

// app_other_ipc.go: Other 号池 IPC 辅助函数(参数解析、事件 emit、预置模型清单)。
// 与 app_account_ipc.go 的 other:xxx case 配套,抽离此处保持 app_account_ipc.go 的 case 分发体量可控。

// parseOtherInputFromArgs 从 IPC args 解析 Other 账号录入参数。
// 支持两种入参形态(前端按需任选):
//  1. 单对象 JSON 字符串(第 0 参为 JSON):{groupId, groupName, baseUrl, apiKey, formats[], label?, defaultModel?}
//     推荐,字段顺序无关,扩展友好。
//  2. 位置参数:[groupID, baseURL, apiKey, groupName?, formatsJSON?, label?, defaultModel?]
//     与 nvidia:add 的位置参数风格对齐(向后兼容场景)。
func parseOtherInputFromArgs(args []interface{}) (account.OtherAccountInput, error) {
	var in account.OtherAccountInput
	if len(args) == 0 {
		return in, errOtherArgs("参数不足:至少需要 groupId/baseUrl/apiKey")
	}
	// 形态 1:第 0 参是 JSON 对象字符串(以 '{' 开头)。
	if s, ok := args[0].(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
		var obj struct {
			GroupID      string   `json:"groupId"`
			GroupName    string   `json:"groupName"`
			BaseURL      string   `json:"baseUrl"`
			APIKey       string   `json:"apiKey"`
			Formats      []string `json:"formats"`
			Label        string   `json:"label"`
			DefaultModel string   `json:"defaultModel"`
		}
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return in, errOtherArgs("解析 Other 录入 JSON 失败: " + err.Error())
		}
		in.GroupID = obj.GroupID
		in.GroupName = obj.GroupName
		in.BaseURL = obj.BaseURL
		in.APIKey = obj.APIKey
		in.Formats = obj.Formats
		in.Label = obj.Label
		in.DefaultModel = obj.DefaultModel
		return in, nil
	}
	// 形态 2:位置参数。
	strAt := func(i int) string {
		if i < len(args) {
			if s, ok := args[i].(string); ok {
				return s
			}
		}
		return ""
	}
	in.GroupID = strAt(0)
	in.BaseURL = strAt(1)
	in.APIKey = strAt(2)
	in.GroupName = strAt(3)
	formatsJSON := strAt(4)
	if strings.TrimSpace(formatsJSON) != "" {
		// formats 既支持 JSON 数组字符串 '["openai","anthropic"]',也支持逗号分隔 'openai,anthropic'。
		if strings.HasPrefix(strings.TrimSpace(formatsJSON), "[") {
			_ = json.Unmarshal([]byte(formatsJSON), &in.Formats)
		} else {
			for _, f := range strings.Split(formatsJSON, ",") {
				if tf := strings.TrimSpace(f); tf != "" {
					in.Formats = append(in.Formats, tf)
				}
			}
		}
	}
	in.Label = strAt(5)
	in.DefaultModel = strAt(6)
	return in, nil
}

// parseOtherFetchArgs 从 other:fetch-models 的 args 解析组标识与可选的直接透传 baseURL/apiKey/formats。
// 支持两种形态:
//  1. 单对象 JSON 字符串(第 0 参):{groupId, baseUrl?, apiKey?, formats?}
//  2. 纯 groupID 字符串(第 0 参):仅组标识,baseURL/apiKey/formats 查号池该组账号。
func parseOtherFetchArgs(args []interface{}) (groupID, baseURL, apiKey string, formats []string, err error) {
	if len(args) == 0 {
		return "", "", "", nil, errOtherArgs("缺少 groupId")
	}
	s, ok := args[0].(string)
	if !ok {
		return "", "", "", nil, errOtherArgs("groupId 必须为字符串")
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") {
		var obj struct {
			GroupID string   `json:"groupId"`
			BaseURL string   `json:"baseUrl"`
			APIKey  string   `json:"apiKey"`
			Formats []string `json:"formats"`
		}
		if e := json.Unmarshal([]byte(trimmed), &obj); e != nil {
			return "", "", "", nil, errOtherArgs("解析 fetch-models JSON 失败: " + e.Error())
		}
		return obj.GroupID, obj.BaseURL, obj.APIKey, obj.Formats, nil
	}
	if trimmed == "" {
		return "", "", "", nil, errOtherArgs("groupId 不能为空")
	}
	return trimmed, "", "", nil, nil
}

// errOtherArgs 是 Other IPC 参数解析错误的构造器,统一加前缀便于日志辨认。
func errOtherArgs(msg string) error {
	return &otherIPCError{msg: msg}
}

type otherIPCError struct{ msg string }

func (e *otherIPCError) Error() string { return "[Other IPC] " + e.msg }
