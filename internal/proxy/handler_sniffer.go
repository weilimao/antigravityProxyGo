package proxy

import (
	"regexp"
	"strconv"
	"strings"
)

// handler_sniffer.go: 官方原生客户端请求嗅探器。
// 从入站 HTTP 请求头（User-Agent）中动态提取官方客户端的版本号（Hub Version）与构建号（Build CL），
// 自动反哺号池配置与前端界面，实现零人工介入的指纹无感热对齐。

var (
	// reHubUA 匹配官方标准 Hub / IDE 客户端 User-Agent:
	// 例: "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)"
	// 或: "antigravity/2.29.1 windows/amd64 ..."
	reHubUA = regexp.MustCompile(`(?i)^antigravity(?:/(?:hub|ide|cli))?/([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
	// reBuildCL 提取括号内的官方构建号: "cl=1015941558"
	reBuildCL = regexp.MustCompile(`(?i)\bcl=([0-9]{7,12})\b`)
)

// SniffAntigravityVersion 从入站 User-Agent 字符串中解析出官方版本号与 CL 构建号。
// 仅对判定为官方原生客户端 (isNativeOfficialAntigravityClient) 的 UA 生效，排除第三方编辑器或插件流量。
func SniffAntigravityVersion(ua string) (version string, buildCL string, ok bool) {
	cleanUA := strings.TrimSpace(ua)
	if cleanUA == "" || !isNativeOfficialAntigravityClient(cleanUA) {
		return "", "", false
	}

	mVer := reHubUA.FindStringSubmatch(cleanUA)
	if len(mVer) < 2 || mVer[1] == "" {
		return "", "", false
	}
	version = mVer[1]

	mCL := reBuildCL.FindStringSubmatch(cleanUA)
	if len(mCL) >= 2 {
		buildCL = mCL[1]
	}

	return version, buildCL, true
}

// CompareVersion 比较两个形如 "x.y.z" 的语义化版本字符串。
// 若 v1 > v2 返回 1; 若 v1 < v2 返回 -1; 若相等返回 0。
func CompareVersion(v1, v2 string) int {
	parts1 := strings.Split(strings.TrimSpace(v1), ".")
	parts2 := strings.Split(strings.TrimSpace(v2), ".")
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}
	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			n1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			n2, _ = strconv.Atoi(parts2[i])
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}
