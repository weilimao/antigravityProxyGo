package relay

import "strings"

// grokPathPrefix.go: Grok(x.ai) 中继入口(/grok 与别名 /xai)的前缀匹配收敛。
//
// 设计与 nvidiaPathPrefix.go 完全同构:/grok 与 /xai 是同一 handleGrok 链路的两个别名前缀,
// 采用与 /nvidia、/vc 同等的精确度,避免短前缀误吞(/grokfoo、/xaichat 等紧跟非斜杠字符不命中)。
//
// 命中条件:path == p(裸前缀,走 handleGrok 内 404 兜底)或 strings.HasPrefix(path, p+"/")。
// 复用一处避免 compat.go(ServeHTTP 大分发)与 server.go 两处硬编码逻辑漂移。

// grokAliasPrefixMatch 判定 path 是否落在 /grok 或 /xai 别名前缀下,
// 供 compat.go(ServeHTTP 大分发)调用点共用(与 nvidiaAliasPrefixMatch 同构)。
func grokAliasPrefixMatch(path string) bool {
	return isGrokAliasPrefix(path, "/grok") || isGrokAliasPrefix(path, "/xai")
}

// isGrokAliasPrefix 判定 path 是否命中单个前缀 p(如 "/grok"/"/xai")。
// 命中条件:path == p(裸前缀,无子路径,走下游 404 兜底,与既有行为对等)
// 或 strings.HasPrefix(path, p+"/")。p+"/" 形式天然排除 "/grokfoo" 这类紧跟非斜杠字符的路径。
func isGrokAliasPrefix(path, p string) bool {
	if path == p {
		return true
	}
	return strings.HasPrefix(path, p+"/")
}
