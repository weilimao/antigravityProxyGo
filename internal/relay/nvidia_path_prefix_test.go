package relay

import "testing"

// nvidia_path_prefix_test.go: 锁定 nvidiaAliasPrefixMatch 前缀收敛语义。
//
// /vc 作为 /nvidia 的纯别名前缀,前缀匹配必须与 /nvidia 等精度:
//   - /nvidia/* 与 /vc/* 子路径:命中
//   - 裸 /nvidia、/vc:命中(走下游 404 兜底,与既有对等)
//   - /vcard、/vcards 等紧跟非斜杠字符:不命中(回归安全:绝不误吞进 NVIDIA 链路)
//   - /v1/*、/responses 等无关前缀:不命中

func TestNvidiaAliasPrefixMatch_NvidiaSubpath(t *testing.T) {
	cases := []string{
		"/nvidia/v1/messages",
		"/nvidia/v1/chat/completions",
		"/nvidia/v1/responses",
		"/nvidia/v1/models",
		"/nvidia/models",
	}
	for _, p := range cases {
		if !nvidiaAliasPrefixMatch(p) {
			t.Fatalf("/nvidia 子路径 %q 应命中", p)
		}
	}
}

func TestNvidiaAliasPrefixMatch_VCSubpath(t *testing.T) {
	cases := []string{
		"/vc/v1/messages",
		"/vc/v1/chat/completions",
		"/vc/v1/responses",
		"/vc/v1/models",
		"/vc/models",
	}
	for _, p := range cases {
		if !nvidiaAliasPrefixMatch(p) {
			t.Fatalf("/vc 别名子路径 %q 应命中", p)
		}
	}
}

// TestNvidiaAliasPrefixMatch_BarePrefix 锁定裸前缀(无子路径)命中语义:
// "/nvidia"、"/vc" 本身命中,统一走 handleNvidia 内 404 兜底,
// 与既有 /nvidia 裸前缀行为对等(不走外层 405 / 下游兜底)。
func TestNvidiaAliasPrefixMatch_BarePrefix(t *testing.T) {
	if !nvidiaAliasPrefixMatch("/nvidia") {
		t.Fatalf("/nvidia 裸前缀应命中(对等既有行为)")
	}
	if !nvidiaAliasPrefixMatch("/vc") {
		t.Fatalf("/vc 裸前缀应命中(对等 /nvidia)")
	}
}

// TestNvidiaAliasPrefixMatch_RejectsNonSlashSuccessor 锁定核心回归安全:
// /vcard、/vcards 等"紧跟非斜杠字符"的路径不得命中 NVIDIA 路由,
// 这是 strings.HasPrefix(path,"/vc") 过宽匹配会误吞的典型场景,修复后必须排除。
func TestNvidiaAliasPrefixMatch_RejectsNonSlashSuccessor(t *testing.T) {
	cases := []string{
		"/vcard",
		"/vcards",
		"/vc1",
		"/vc-test",
		"/nvidiax",
		"/nvidiapro",
	}
	for _, p := range cases {
		if nvidiaAliasPrefixMatch(p) {
			t.Fatalf("紧跟非斜杠字符的路径 %q 不应命中 NVIDIA 路由(排除误吞)", p)
		}
	}
}

func TestNvidiaAliasPrefixMatch_UnrelatedPrefixes(t *testing.T) {
	cases := []string{
		"/v1/messages",
		"/v1/chat/completions",
		"/v1internal:generateContent",
		"/responses",
		"/api/stats",
		"/",
		"",
	}
	for _, p := range cases {
		if nvidiaAliasPrefixMatch(p) {
			t.Fatalf("无关前缀 %q 不应命中 NVIDIA 路由", p)
		}
	}
}
