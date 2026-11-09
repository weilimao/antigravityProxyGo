package quota

import (
	"encoding/json"
	"strings"
)

// desktopQuotaUserAgent 是抓包里桌面端 retrieveUserQuotaSummary 的请求头。
// 抓包列表里没有可用样本时，额度刷新按这一份格式发往上游。
const desktopQuotaUserAgent = "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)"

func (q *QuotaService) lookupTemplate(pathNeedle string) (map[string]string, []byte, bool) {
	if q == nil {
		return nil, nil, false
	}
	q.RLock()
	fn := q.requestTemplate
	q.RUnlock()
	if fn == nil {
		return nil, nil, false
	}
	headers, body, ok := fn(pathNeedle)
	if !ok || !usableDesktopTemplate(headers) {
		return nil, nil, false
	}
	return headers, body, true
}

func usableDesktopTemplate(headers map[string]string) bool {
	ua := strings.ToLower(headerValue(headers, "User-Agent"))
	if !strings.Contains(ua, "antigravity/") {
		return false
	}
	if strings.Contains(ua, "google-api-nodejs-client") || strings.Contains(ua, "plugin") || strings.Contains(ua, "extension") {
		return false
	}
	return true
}

func (q *QuotaService) antigravityUpstreamHeaders(pathNeedle, accessToken string) map[string]string {
	ua := q.GetAntigravityUA()
	if ua == "" {
		ua = desktopQuotaUserAgent
	}
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if captured, _, ok := q.lookupTemplate(pathNeedle); ok {
		for k, v := range captured {
			if skipReplayHeader(k, v) {
				continue
			}
			headers[k] = v
		}
		if got := headerValue(captured, "User-Agent"); got != "" {
			ua = got
		}
	} else if pathNeedle != "retrieveUserQuotaSummary" {
		if captured, _, ok := q.lookupTemplate("retrieveUserQuotaSummary"); ok {
			if got := headerValue(captured, "User-Agent"); got != "" {
				ua = got
			}
		}
	}
	headers["User-Agent"] = ua
	headers["Authorization"] = "Bearer " + accessToken
	return headers
}

func (q *QuotaService) loadCodeAssistBody(ua string) map[string]interface{} {
	if _, raw, ok := q.lookupTemplate("loadCodeAssist"); ok && len(raw) > 0 {
		var obj map[string]interface{}
		if json.Unmarshal(raw, &obj) == nil && obj != nil {
			return obj
		}
	}
	ver := versionFromAntigravityUA(ua)
	if ver == "" {
		ver = q.GetAntigravityVersion()
	}
	metadata := map[string]interface{}{
		"ideType":    "ANTIGRAVITY",
		"ideVersion": ver,
		"ideName":    "antigravity",
	}
	if strings.Contains(strings.ToLower(ua), "windows") && strings.Contains(strings.ToLower(ua), "amd64") {
		metadata["platform"] = "WINDOWS_AMD64"
	}
	return map[string]interface{}{"metadata": metadata}
}

func (q *QuotaService) retrieveQuotaBody(project string) map[string]interface{} {
	if _, raw, ok := q.lookupTemplate("retrieveUserQuotaSummary"); ok && len(raw) > 0 {
		var obj map[string]interface{}
		if json.Unmarshal(raw, &obj) == nil && obj != nil {
			if project != "" {
				obj["project"] = project
			} else {
				delete(obj, "project")
			}
			return obj
		}
	}
	body := map[string]interface{}{}
	if project != "" {
		body["project"] = project
	}
	return body
}

func skipReplayHeader(key, value string) bool {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	switch lowerKey {
	case "authorization", "cookie", "content-length", "host", "connection", "proxy-connection", "accept-encoding", "transfer-encoding",
		"x-client-data", "x-session-id", "x-session-affinity", "session-id", "thread-id", "turn-id", "via", "forwarded", "x-real-ip", "x-request-id":
		return true
	}
	for _, prefix := range []string{
		"x-relay-", "x-antigravity-", "sec-ch-ua", "sec-fetch-",
		"x-stainless-", "anthropic-", "openai-", "x-claude-", "x-codex-", "x-forwarded-", "cf-",
	} {
		if strings.HasPrefix(lowerKey, prefix) {
			return true
		}
	}
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "[REDACTED]"
}

func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func versionFromAntigravityUA(ua string) string {
	lower := strings.ToLower(ua)
	idx := strings.Index(lower, "antigravity/")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(ua[idx+len("antigravity/"):])
	if fields := strings.Fields(rest); len(fields) > 0 {
		rest = fields[0]
	}
	if cut := strings.Index(rest, "("); cut >= 0 {
		rest = rest[:cut]
	}
	parts := strings.Split(rest, "/")
	ver := parts[len(parts)-1]
	if !strings.Contains(ver, ".") {
		return ""
	}
	return ver
}
