package relay

import "strings"

// nvidiaPathPrefix.go: NVIDIA 中继入口(/nvidia 与别名 /vc)的前缀匹配收敛。
//
// 背景:/vc 作为 /nvidia 的纯别名前缀,分发到同一 handleNvidia 链路。但 /vc 是短前缀,
// 若沿用 strings.HasPrefix(path, "/vc") 会误吞 /vcard、/vcards 等一切 /vc 开头路径,
// 与 /nvidia(前缀本身就长、碰撞面小)的精确度不对等。
//
// 本文件抽出 nvidiaAliasPrefixMatch,采用与 /nvidia 同等的精确度:
//   - 完全相等("/nvidia"、"/vc" 本身) :命中(走 handleNvidia 内 404 兜底,与既有 /nvidia 裸前缀对等)
//   - 紧跟子路径("/nvidia/v1/messages"、"/vc/v1/models") :命中
//   - 紧跟非斜杠字符("/vcard"、"/vcards") :不命中(回归安全:绝不误判为 NVIDIA 路由)
// 复用一处避免 compat.go 与 server.go 两处硬编码逻辑漂移。

// nvidiaAliasPrefixMatch 判定 path 是否落在 /nvidia 或 /vc 别名前缀下,
// 供 compat.go(ServeHTTP 大分发)与 server.go(中继路由前置分流)两处调用点共用。
// 语义对齐 net/http.StripPrefix:前缀必须以 / 结尾,或 path 恰好等于去掉尾 / 的前缀。
func nvidiaAliasPrefixMatch(path string) bool {
	return isNvidiaAliasPrefix(path, "/nvidia") || isNvidiaAliasPrefix(path, "/vc")
}

// isNvidiaAliasPrefix 判定 path 是否命中单个前缀 p(如 "/nvidia"/"/vc")。
// 命中条件:path == p(裸前缀,无子路径,走下游 404 兜底,与既有行为对等)
// 或 strings.HasPrefix(path, p+"/")。p+"/" 形式天然排除 "/vcard" 这类紧跟非斜杠字符的路径。
func isNvidiaAliasPrefix(path, p string) bool {
	if path == p {
		return true
	}
	return strings.HasPrefix(path, p+"/")
}
