package relay

import "strings"

// workbuddyPathPrefix.go: WorkBuddy 中继入口 (/workbuddy 与别名 /wb) 的前缀匹配收敛。
//
// 命中条件: path == p 或 strings.HasPrefix(path, p+"/")。
// 精确匹配，避免误吞 /workbuddyfoo 之类的路径。

func workbuddyAliasPrefixMatch(path string) bool {
	return isWorkBuddyAliasPrefix(path, "/workbuddy") || isWorkBuddyAliasPrefix(path, "/wb")
}

func isWorkBuddyAliasPrefix(path, p string) bool {
	if path == p {
		return true
	}
	return strings.HasPrefix(path, p+"/")
}
