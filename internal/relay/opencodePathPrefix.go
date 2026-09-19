package relay

import "strings"

// opencodePathPrefix.go: OpenCode 中继入口 (/opencode 与别名 /oc) 的前缀匹配收敛。
//
// 命中条件: path == p 或 strings.HasPrefix(path, p+"/")。
// 精确匹配，避免误吞 /opencodexxx 之类的路径。

func opencodeAliasPrefixMatch(path string) bool {
	return isOpenCodeAliasPrefix(path, "/opencode") || isOpenCodeAliasPrefix(path, "/oc")
}

func isOpenCodeAliasPrefix(path, p string) bool {
	if path == p {
		return true
	}
	return strings.HasPrefix(path, p+"/")
}
