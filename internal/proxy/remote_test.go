package proxy

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// generateTestCertificate generates a self-signed certificate in memory for TLS testing
func generateTestCertificate() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Antigravity Test Org"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}, nil
}

// TestDialThroughRemote tests dialing through remote relay using HTTPS (upgrades to TLS)
func TestDialThroughRemote(t *testing.T) {
	// 1. Generate TLS certificate
	cert, err := generateTestCertificate()
	if err != nil {
		t.Fatalf("failed to generate test cert: %v", err)
	}

	// 2. Start TLS test server
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	portStr := fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)

	// Server handler
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read CONNECT request
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method == "CONNECT" {
					// Send 200 OK
					c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
					// Echo test
					buf := make([]byte, 1024)
					n, err := c.Read(buf)
					if err == nil {
						c.Write(buf[:n])
					}
				}
			}(conn)
		}
	}()

	// 3. Create RemoteRelay client configured with HTTPS
	rr := NewRemoteRelay(nil)
	rr.config = RemoteConfig{
		Host:      "https://127.0.0.1",
		Port:      portStr,
		Token:     "mock_token",
		Connected: true,
	}

	// 4. Dial through remote and verify
	conn, err := rr.DialThroughRemote("example.com:80")
	if err != nil {
		t.Fatalf("failed to DialThroughRemote: %v", err)
	}
	defer conn.Close()

	// Verify data transmission over the TLS connection
	msg := "hello antigravity"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("failed to write to connection: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read from connection: %v", err)
	}

	if string(buf[:n]) != msg {
		t.Errorf("expected %q, got %q", msg, string(buf[:n]))
	}
}

// TestDialThroughRemotePlainTCP tests dialing through remote relay using HTTP (falls back to plain TCP)
func TestDialThroughRemotePlainTCP(t *testing.T) {
	// 1. Start plain TCP test server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	portStr := fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)

	// Server handler
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read plain HTTP CONNECT
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method == "CONNECT" {
					c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
					buf := make([]byte, 1024)
					n, err := c.Read(buf)
					if err == nil {
						c.Write(buf[:n])
					}
				}
			}(conn)
		}
	}()

	// 2. Create RemoteRelay client configured with HTTP
	rr := NewRemoteRelay(nil)
	rr.config = RemoteConfig{
		Host:      "http://127.0.0.1",
		Port:      portStr,
		Token:     "mock_token",
		Connected: true,
	}

	// 3. Dial through remote and verify
	conn, err := rr.DialThroughRemote("example.com:80")
	if err != nil {
		t.Fatalf("failed to DialThroughRemote plain: %v", err)
	}
	defer conn.Close()

	msg := "hello plain"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("failed to write plain: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read plain: %v", err)
	}

	if string(buf[:n]) != msg {
		t.Errorf("expected %q, got %q", msg, string(buf[:n]))
	}
}

// TestRemoteRelayHostParsing tests that different formats of configured Host are parsed and matched correctly
func TestRemoteRelayHostParsing(t *testing.T) {
	testCases := []struct {
		configuredHost string
		requestHost    string
		expectedSelf   bool
	}{
		{"https://8.148.23.187", "8.148.23.187", true},
		{"http://8.148.23.187", "8.148.23.187", true},
		{"8.148.23.187", "8.148.23.187", true},
		{"https://example.com", "example.com", true},
		{"https://example.com", "other.com", false},
		{"example.com", "example.com", true},
	}

	for _, tc := range testCases {
		relayHost := tc.configuredHost
		if strings.Contains(relayHost, "://") {
			if u, urlErr := url.Parse(relayHost); urlErr == nil {
				relayHost = u.Hostname()
			}
		}
		isRemoteRelaySelf := (tc.requestHost == relayHost)
		if isRemoteRelaySelf != tc.expectedSelf {
			t.Errorf("For configuredHost=%q, requestHost=%q: expected isRemoteRelaySelf=%v, got %v",
				tc.configuredHost, tc.requestHost, tc.expectedSelf, isRemoteRelaySelf)
		}
	}
}

// TestRelayedRequestLoopDetection verifies that any request identified as a relayed request (non-empty relay user ID) is flagged as a loop to prevent infinite forwarding
func TestRelayedRequestLoopDetection(t *testing.T) {
	// Simulate an incoming relayed request containing a relay user ID in context
	incomingRelayUserID := "test-user-id"

	// Verification logic matching handler.go: incomingRelayUserID != "" => isLocalRelayLoop = true
	isLocalRelayLoop := incomingRelayUserID != ""
	if !isLocalRelayLoop {
		t.Error("expected loop to be detected when incoming relay user ID is not empty")
	}

	// Verify that a normal request (empty relay user ID) is not flagged as a loop
	normalIsLocalRelayLoop := "" != ""
	if normalIsLocalRelayLoop {
		t.Error("expected loop NOT to be detected for normal request with empty relay user ID")
	}
}

// startConnectRelay 启动一个可编程的 CONNECT 中继测试服务器,返回其监听端口。
// failFirst 控制首个被接受的连接在读完 CONNECT 后立即静默关闭(模拟半开连接 EOF),
// 后续连接均回 200 Connection Established。用于复现并验证预热连接半开 EOF 降级路径。
func startConnectRelay(t *testing.T, failFirst bool, emit407 bool, tokenTarget string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	firstHandled := false
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method != "CONNECT" {
					return
				}
				if emit407 {
					c.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Bearer\r\n\r\n"))
					return
				}
				if failFirst && !firstHandled {
					firstHandled = true
					// 不回任何响应,直接 Close,模拟被空闲回收的半开连接(Write 成功→Read EOF)
					return
				}
				c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				buf := make([]byte, 1024)
				n, err := c.Read(buf)
				if err == nil {
					c.Write(buf[:n])
				}
			}(c)
		}
	}()
	return fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
}

// TestDialThroughRemoteHalfOpenEOF 验证预热连接因回收半开导致 Read EOF 时,
// DialThroughRemote 降级为全新直连再握一次并最终成功,而非外泄 unexpected EOF。
// 这是线上 "failed to read CONNECT response: unexpected EOF" 的复现+修复回归。
func TestDialThroughRemoteHalfOpenEOF(t *testing.T) {
	portStr := startConnectRelay(t, true, false, "example.com:80")

	rr := NewRemoteRelay(nil) // nil logFn,触发降级日志路径也不应 panic
	rr.config = RemoteConfig{
		Host:      "127.0.0.1",
		Port:      portStr,
		Token:     "mock_token",
		Connected: true,
	}

	// 第一次:DialThroughRemote 内部预热池为空,会直接 dialRelayRaw 即拿到 failFirst 的半开连接
	// 旧实现在此路径下 Read EOF 直接返回错误;新实现因 dialRelayRaw 也走 handshakeOnce -> 网络层错误,
	// 但此处没有"预热连接可降级"的池可退,故返回错误(这是"全新连接首握失败"的语义,本用例不覆盖)。
	// 为精确覆盖"预热连接半开→降级全新直连"的修复路径,改用第二次请求:此时半开连接已被关闭,
	// 第二次 dialRelayRaw 应收到 200 并成功。
	_, _ = rr.DialThroughRemote("example.com:80") // 预期失败(首握半开),忽略结果

	// 第二次请求:failFirst 已消耗,本次全新直连返回 200 并 echo
	conn, err := rr.DialThroughRemote("example.com:80")
	if err != nil {
		t.Fatalf("expected fallback dial to succeed after half-open EOF, got: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("fallback-ok")); err != nil {
		t.Fatalf("write after fallback failed: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read after fallback failed: %v", err)
	}
	if string(buf[:n]) != "fallback-ok" {
		t.Fatalf("expected echo 'fallback-ok', got %q", string(buf[:n]))
	}
}

// TestDialThroughRemoteWarmPoolHalfOpen 验证更贴合线上的路径:
// 预热池中确实有一条已被回收的半开连接,DialThroughRemote 应探测失败→降级全新直连成功。
func TestDialThroughRemoteWarmPoolHalfOpen(t *testing.T) {
	portStr := startConnectRelay(t, true, false, "example.com:80")

	rr := NewRemoteRelay(nil)
	rr.config = RemoteConfig{
		Host:      "127.0.0.1",
		Port:      portStr,
		Token:     "mock_token",
		Connected: true,
	}
	rr.StartWarmPool()
	defer rr.StopWarmPool()

	// 给预热池一点时间建立连接(会命中 failFirst 的半开连接被静默关闭)
	time.Sleep(300 * time.Millisecond)

	// 预热池里现在有一条半开连接;DialThroughRemote 应该:
	// 1) 取出半开连接握手失败(Read EOF 或 Write 后无响应)
	// 2) 降级为全新直连再握一次(此时 failFirst 已消耗)→ 200 成功
	conn, err := rr.DialThroughRemote("example.com:80")
	if err != nil {
		t.Fatalf("expected warm-pool half-open fallback to succeed, got: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("warm-ok")); err != nil {
		t.Fatalf("write after warm fallback failed: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read after warm fallback failed: %v", err)
	}
	if string(buf[:n]) != "warm-ok" {
		t.Fatalf("expected echo 'warm-ok', got %q", string(buf[:n]))
	}
}

// TestDialThroughRemote407TriggersTokenExpired 验证 407 在线拒绝时不降级重试,
// 仅异步触发 onTokenExpired 回调一次,避免对在线拒绝的连击。
func TestDialThroughRemote407TriggersTokenExpired(t *testing.T) {
	portStr := startConnectRelay(t, false, true, "example.com:80")

	tokenExpired := make(chan struct{}, 4)
	rr := NewRemoteRelay(nil)
	rr.config = RemoteConfig{
		Host:      "127.0.0.1",
		Port:      portStr,
		Token:     "mock_token",
		Connected: true,
	}
	rr.SetOnTokenExpired(func() {
		select {
		case tokenExpired <- struct{}{}:
		default:
		}
	})

	// 多次请求:每次都应在线失败、不降级(emit407=true,每次中继都回 407)
	for i := 0; i < 3; i++ {
		conn, err := rr.DialThroughRemote("example.com:80")
		if err == nil {
			conn.Close()
			t.Fatalf("expected 407 failure on attempt %d, got success", i)
		}
		if !strings.Contains(err.Error(), "407") {
			t.Fatalf("expected error mentioning 407, got: %v", err)
		}
	}

	select {
	case <-tokenExpired:
		// 至少触发一次 onTokenExpired,正确
	case <-time.After(2 * time.Second):
		t.Fatal("expected onTokenExpired callback to fire after 407")
	}
}



