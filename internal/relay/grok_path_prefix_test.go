package relay

import "testing"

// grok_path_prefix_test.go: 锁定 grokAliasPrefixMatch 前缀收敛语义。
//
// /xai 作为 /grok 的纯别名前缀,前缀匹配必须与 /grok 等精度:
//   - /grok/* 与 /xai/* 子路径:命中(走 handleGrok 链路)
//   - 裸 /grok、/xai:命中(走下游 404 兜底,与 /nvidia、/vc 裸前缀对等)
//   - /grokfoo、/xaichat 等紧跟非斜杠字符:不命中(回归安全:绝不误吞进 Grok 链路)
//   - /v1/*、/nvidia/*、/vc/* 等无关前缀:不命中
//
// 与 nvidia_path_prefix_test.go 同构,仅替换别名前缀。

func TestGrokAliasPrefixMatch_GrokSubpath(t *testing.T) {
	cases := []string{
		"/grok/v1/messages",
		"/grok/v1/chat/completions",
		"/grok/v1/responses",
		"/grok/v1/models",
		"/grok/models",
	}
	for _, p := range cases {
		if !grokAliasPrefixMatch(p) {
			t.Fatalf("/grok 子路径 %q 应命中", p)
		}
	}
}

func TestGrokAliasPrefixMatch_XAISubpath(t *testing.T) {
	cases := []string{
		"/xai/v1/messages",
		"/xai/v1/chat/completions",
		"/xai/v1/responses",
		"/xai/v1/models",
		"/xai/models",
	}
	for _, p := range cases {
		if !grokAliasPrefixMatch(p) {
			t.Fatalf("/xai 别名子路径 %q 应命中", p)
		}
	}
}

// TestGrokAliasPrefixMatch_BarePrefix 锁定裸前缀(无子路径)命中语义:
// "/grok"、"/xai" 本身命中,统一走 handleGrok 内 404 兜底,
// 与既有 /nvidia、/vc 裸前缀行为对等(不走外层 405 / 下游兜底)。
func TestGrokAliasPrefixMatch_BarePrefix(t *testing.T) {
	if !grokAliasPrefixMatch("/grok") {
		t.Fatalf("/grok 裸前缀应命中(对等既有行为)")
	}
	if !grokAliasPrefixMatch("/xai") {
		t.Fatalf("/xai 裸前缀应命中(对等 /grok)")
	}
}

// TestGrokAliasPrefixMatch_RejectsNonSlashSuccessor 锁定核心回归安全:
// /grokfoo、/xaichat 等"紧跟非斜杠字符"的路径不得命中 Grok 路由,
// 这是 strings.HasPrefix(path,"/grok") 过宽匹配会误吞的典型场景,修复后必须排除。
func TestGrokAliasPrefixMatch_RejectsNonSlashSuccessor(t *testing.T) {
	cases := []string{
		"/grokfoo",
		"/grokfoo/v1/messages",
		"/grokx",
		"/grok-pro",
		"/xaichat",
		"/xaiaudio",
		"/xai1",
		"/xai-code",
		"/grokbeta/v1/messages",
	}
	for _, p := range cases {
		if grokAliasPrefixMatch(p) {
			t.Fatalf("紧跟非斜杠字符的路径 %q 不应命中 Grok 路由(排除误吞)", p)
		}
	}
}

// TestGrokAliasPrefixMatch_UnrelatedPrefixes 锁定无关前缀绝不命中 Grok:
// /nvidia/* 走 NVIDIA 链路、/vc/* 走 NVIDIA 别名、/v1/* 走 Google 族直连,
// 它们与 Grok 前缀物理隔离,确保 grokAliasPrefixMatch 不误吞其它号池入站。
func TestGrokAliasPrefixMatch_UnrelatedPrefixes(t *testing.T) {
	cases := []string{
		"/v1/messages",
		"/v1/chat/completions",
		"/v1internal:generateContent",
		"/responses",
		"/api/stats",
		"/nvidia/v1/messages",
		"/nvidia/models",
		"/vc/v1/chat/completions",
		"/route/v1/chat/completions",
		"/",
		"",
	}
	for _, p := range cases {
		if grokAliasPrefixMatch(p) {
			t.Fatalf("无关前缀 %q 不应命中 Grok 路由", p)
		}
	}
}
