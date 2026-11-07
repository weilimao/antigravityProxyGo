package proxy

import (
	"antigravity-proxy/internal/account"
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// v1internal_guard.go: 18443 代理层 v1internal 周边接口防泄漏拦截与跨账号 JSON 指纹脱敏重写。
// 职责：
//  1. 拦截客户端遥测上报接口（recordCodeAssistMetrics 等），阻断将号池 Account B 生成的响应指标/RequestID 用客户端 Account A 的 Token 回传给 Google；
//  2. 在免登录模式下将 loadCodeAssist / fetchAvailableModels / countTokens 纳入号池选号，防止无效占位 Token 直穿 Google；
//  3. 在原生透传（nativePassthrough）及 429/503/404 跨号重试时，原位重写 JSON 载荷中的 project、sessionId 与 requestId，切断跨账号静态指纹关联。

// isV1InternalTelemetryEndpoint 判定是否为客户端向 Google 上报遥测/指标的 v1internal 接口。
func isV1InternalTelemetryEndpoint(targetPath string) bool {
	p := strings.ToLower(targetPath)
	if !strings.Contains(p, "v1internal") {
		return false
	}
	return strings.Contains(p, "recordcodeassistmetrics") ||
		strings.Contains(p, "recordmetrics") ||
		strings.Contains(p, "reportmetrics") ||
		strings.Contains(p, "logevent")
}

// shouldRouteV1InternalThroughPool 判定当前请求是否应当分配号池账号进行转发。
// - 模型生成请求（generateContent / streamGenerateContent / predict / agent / aiplatform）始终走号池；
// - 当客户端处于免登录状态（!isGoogleAuth）时，周边能力请求（loadCodeAssist / fetchAvailableModels / countTokens）
//   也自动接入号池代发，避免把非法的免登占位 Token 发送给 Google 上游。
func shouldRouteV1InternalThroughPool(targetPath, targetHost string, isGoogleAuth bool) bool {
	if isRealModelRequest(targetPath) || isAgentRequest(targetPath) || targetHost == "aiplatform.googleapis.com" {
		return true
	}
	if !isGoogleAuth && strings.Contains(strings.ToLower(targetPath), "v1internal") {
		p := strings.ToLower(targetPath)
		if strings.Contains(p, "loadcodeassist") ||
			strings.Contains(p, "fetchavailablemodels") ||
			strings.Contains(p, "counttokens") {
			return true
		}
	}
	return false
}

// handleV1InternalPeripheralGuard 在进入上游转发前拦截高风险的 v1internal 周边遥测与免登配额请求。
// 返回 true 表示已在本地完成响应，调用方应直接 return。
func (h *ProxyHandler) handleV1InternalPeripheralGuard(w http.ResponseWriter, targetPath string, isGoogleAuth bool) bool {
	if isV1InternalTelemetryEndpoint(targetPath) {
		if h != nil && h.logFn != nil {
			h.logFn(fmt.Sprintf("🛡️ [防关联拦截] 本地静默消化客户端遥测上报 (%s)，阻断跨账号指标回传", targetPath))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
		return true
	}

	if !isGoogleAuth && strings.Contains(strings.ToLower(targetPath), "retrieveuserquota") {
		if h != nil && h.logFn != nil {
			h.logFn(fmt.Sprintf("🛡️ [防关联拦截] 本地合成免登录客户端配额响应 (%s)", targetPath))
		}
		mockQuota := []byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"displayName":"Five Hour Limit","remainingFraction":1.0}]}]}`)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(mockQuota)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mockQuota)
		return true
	}

	return false
}

// rewriteNativePassthroughIdentity 在保持原生客户端 JSON 字节序与未知字段无损的前提下，
// 原位重写跨账号敏感标识：
//  1. 顶层 "project"：对齐为当前出站号池账号的专属 Project ID；
//  2. "sessionId"（若客户端请求体中携带）：重写为当前号池账号派生的 19 位负十进制整数 sessionId，
//     防止多个号池账号向 Google 上报同一个本地客户端进程的静态 sessionId；
//  3. 顶层 "requestId"（当 attemptIndex > 0 发生 429/503/404 切号重试时）：重写为全新的标准 UUIDv4 requestId，
//     阻断 Account A 失败后 100ms 内 Account B 携带完全相同 requestId 重试的强反代关联特征。
func rewriteNativePassthroughIdentity(
	body []byte,
	poolAccount *account.Account,
	targetProject string,
	 currentBodyProject string,
	sessionSeed string,
	attemptIndex int,
	allowsProjectField bool,
) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	out := body
	changed := false

	if allowsProjectField && targetProject != "" && currentBodyProject != targetProject {
		if replaced, ok := replaceTopLevelJSONString(out, "project", targetProject); ok {
			out = replaced
			changed = true
		}
	}

	accKey := ""
	if poolAccount != nil {
		accKey = strings.TrimSpace(poolAccount.ID)
		if accKey == "" {
			accKey = strings.TrimSpace(poolAccount.Email)
		}
	}

	if accKey != "" && bytes.Contains(out, []byte(`"sessionId"`)) {
		derivedSessionID := account.AccountSessionID(accKey, sessionSeed)
		if replaced, ok := replaceJSONStringFieldAnywhere(out, "sessionId", derivedSessionID); ok {
			out = replaced
			changed = true
		}
	}

	if attemptIndex > 0 && bytes.Contains(out, []byte(`"requestId"`)) {
		newReqID := account.NewAntigravityRequestID("agent")
		if replaced, ok := replaceTopLevelJSONString(out, "requestId", newReqID); ok {
			out = replaced
			changed = true
		}
	}

	return out, changed
}

// replaceJSONStringFieldAnywhere 在不改变 JSON 任何其余字节与键顺序的前提下，
// 扫描并原位替换指定字段（如嵌套在 request.sessionId 中的 "sessionId"）。
// 同时兼容原有值为字符串形式（"sessionId": "..."）或数值形式（"sessionId": -3750...），
// 统一安全替换为带引号的目标字符串。
func replaceJSONStringFieldAnywhere(body []byte, field, value string) ([]byte, bool) {
	needle := []byte(`"` + field + `"`)
	searchFrom := 0
	for searchFrom < len(body) {
		idx := bytes.Index(body[searchFrom:], needle)
		if idx < 0 {
			return body, false
		}
		keyStart := searchFrom + idx
		afterKey := skipJSONWS(body, keyStart+len(needle))
		if afterKey >= len(body) || body[afterKey] != ':' {
			searchFrom = keyStart + len(needle)
			continue
		}
		valStart := skipJSONWS(body, afterKey+1)
		if valStart >= len(body) {
			searchFrom = afterKey + 1
			continue
		}

		var valEnd int
		var ok bool
		if body[valStart] == '"' {
			_, valEnd, ok = scanJSONString(body, valStart)
		} else if body[valStart] == '-' || (body[valStart] >= '0' && body[valStart] <= '9') {
			valEnd, ok = skipJSONValue(body, valStart)
		} else {
			searchFrom = afterKey + 1
			continue
		}
		if !ok {
			return body, false
		}
		quoted := []byte(strconv.Quote(value))
		out := make([]byte, 0, len(body)-(valEnd-valStart)+len(quoted))
		out = append(out, body[:valStart]...)
		out = append(out, quoted...)
		out = append(out, body[valEnd:]...)
		return out, true
	}
	return body, false
}
