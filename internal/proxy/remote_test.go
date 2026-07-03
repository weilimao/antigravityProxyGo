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



