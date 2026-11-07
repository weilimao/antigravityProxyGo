package netutil

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestAccountClientIsolation(t *testing.T) {
	t.Cleanup(func() {
		ResetAllAccountTransports()
	})

	clientA1 := GetAccountClient("acc-alpha", 10*time.Second)
	clientA2 := GetAccountClient("acc-alpha", 10*time.Second)
	clientB := GetAccountClient("acc-beta", 10*time.Second)

	trA1 := GetAccountTransport("acc-alpha")
	trB := GetAccountTransport("acc-beta")
	trShared := GetAccountTransport("")

	if clientA1.Transport != clientA2.Transport {
		t.Fatalf("expected same account key to reuse the same *http.Transport instance")
	}

	if clientA1.Transport == clientB.Transport {
		t.Fatalf("expected different accounts to have isolated *http.Client transports")
	}

	if trA1 == trB {
		t.Fatalf("expected different accounts to have isolated *http.Transport instances")
	}

	if trA1 == trShared || trB == trShared {
		t.Fatalf("expected per-account Transport to be isolated from global sharedTransport")
	}

	if trA1.TLSClientConfig == nil || trB.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be initialized on isolated transports")
	}

	if trA1.ForceAttemptHTTP2 || trB.ForceAttemptHTTP2 {
		t.Fatalf("expected ForceAttemptHTTP2 to be false to match official CreateCloudCodeHTTPClient")
	}

	if trA1.TLSClientConfig.ClientSessionCache != nil || trB.TLSClientConfig.ClientSessionCache != nil {
		t.Fatalf("expected ClientSessionCache to be nil to avoid emitting session_ticket (0x0023) and psk_key_exchange_modes (0x002d) extensions")
	}

	// 验证单账号清理后重新分配新 Transport
	CloseAccountTransport("acc-alpha")
	trA3 := GetAccountTransport("acc-alpha")
	if trA3 == trA1 {
		t.Fatalf("expected new Transport after CloseAccountTransport")
	}

	// 验证全局重置后所有账号重新分配新 Transport
	ResetAllAccountTransports()
	trB2 := GetAccountTransport("acc-beta")
	if trB2 == trB {
		t.Fatalf("expected new Transport after ResetAllAccountTransports")
	}
}

// captureRawClientHello 启动本地 TCP 监听器捕获指定 Transport 发出的原始 TLS ClientHello 握手报文并解析扩展列表。
func captureRawClientHello(t *testing.T, tr *http.Transport) (ciphers []uint16, extensions []uint16) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local tcp: %v", err)
	}
	defer ln.Close()

	rawCh := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(rawCh)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

		var hdr [5]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			close(rawCh)
			return
		}
		recLen := int(binary.BigEndian.Uint16(hdr[3:5]))
		payload := make([]byte, recLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			close(rawCh)
			return
		}
		rawCh <- payload
	}()

	trCopy := tr.Clone()
	trCopy.Proxy = nil
	trCopy.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+ln.Addr().String()+"/v1internal:loadCodeAssist", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	client := &http.Client{Transport: trCopy, Timeout: 2 * time.Second}
	_, _ = client.Do(req)

	payload, ok := <-rawCh
	if !ok || len(payload) < 42 {
		t.Fatalf("failed to capture raw ClientHello payload (len=%d)", len(payload))
	}

	// TLS Handshake: [0]=0x01 (ClientHello), [1..3]=length, [4..5]=client_version, [6..37]=random
	if payload[0] != 0x01 {
		t.Fatalf("expected HandshakeType ClientHello (0x01), got 0x%02x", payload[0])
	}
	pos := 38
	sessionIDLen := int(payload[pos])
	pos += 1 + sessionIDLen

	cipherSuitesLen := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	for i := 0; i < cipherSuitesLen; i += 2 {
		ciphers = append(ciphers, binary.BigEndian.Uint16(payload[pos+i:pos+i+2]))
	}
	pos += cipherSuitesLen

	compMethodsLen := int(payload[pos])
	pos += 1 + compMethodsLen

	if pos+2 <= len(payload) {
		extTotalLen := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
		pos += 2
		end := pos + extTotalLen
		for pos+4 <= end && pos+4 <= len(payload) {
			extType := binary.BigEndian.Uint16(payload[pos : pos+2])
			extLen := int(binary.BigEndian.Uint16(payload[pos+2 : pos+4]))
			extensions = append(extensions, extType)
			pos += 4 + extLen
		}
	}
	return ciphers, extensions
}

// TestNewIsolatedTransport_TLSClientHelloMatchesOfficial 验证 NewIsolatedTransport 产生的真实 TLS ClientHello
// 与官方 language_server.exe 的 CreateCloudCodeHTTPClient 产生的 CipherSuites 及 Extensions 100% 一致。
func TestNewIsolatedTransport_TLSClientHelloMatchesOfficial(t *testing.T) {
	// 1. 构造与官方 CreateCloudCodeHTTPClient (0x20f6c60) 逐字一致的基准 Transport
	officialTr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// 2. 获取代理号池账号使用的 NewIsolatedTransport
	proxyIsolatedTr := NewIsolatedTransport()

	wantCiphers, wantExts := captureRawClientHello(t, officialTr)
	gotCiphers, gotExts := captureRawClientHello(t, proxyIsolatedTr)

	if !reflect.DeepEqual(gotCiphers, wantCiphers) {
		t.Fatalf("TLS ClientHello CipherSuites mismatch:\ngot:  %v\nwant: %v", gotCiphers, wantCiphers)
	}
	if !reflect.DeepEqual(gotExts, wantExts) {
		t.Fatalf("TLS ClientHello Extensions mismatch:\ngot:  %#x\nwant: %#x", gotExts, wantExts)
	}

	// 3. 显式断言不包含会改变 JA3/JA4 指纹的 ALPN (0x0010)、session_ticket (0x0023)、psk_key_exchange_modes (0x002d)
	forbiddenExts := map[uint16]string{
		0x0010: "application_layer_protocol_negotiation (ALPN)",
		0x0023: "session_ticket",
		0x002d: "psk_key_exchange_modes",
		0x0029: "pre_shared_key",
	}
	for _, ext := range gotExts {
		if name, forbidden := forbiddenExts[ext]; forbidden {
			t.Errorf("TLS ClientHello must NOT contain extension 0x%04x (%s), but it was present in %#x", ext, name, gotExts)
		}
	}
}
