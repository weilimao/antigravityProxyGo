package relay

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type contextKey string

const RelayUserCtxKey contextKey = "relayUserID"

type RelayServer struct {
	mu                sync.Mutex
	engine            http.Handler
	authMgr           *AuthManager
	apiHandler        *APIHandler
	compatHandler     *APICompatHandler
	server            *http.Server
	listener          net.Listener
	trackedListener   *trackedListener
	logFn             func(string)
	isRunning         bool
	isTLS             bool
	relayUserCtxKey   interface{}
	relayAPIKeyCtxKey interface{}
}

func NewRelayServer(
	engine http.Handler,
	authMgr *AuthManager,
	apiHandler *APIHandler,
	compatHandler *APICompatHandler,
	logFn func(string),
	userCtxKey interface{},
	apiKeyCtxKey interface{},
) *RelayServer {
	return &RelayServer{
		engine:            engine,
		authMgr:           authMgr,
		apiHandler:        apiHandler,
		compatHandler:     compatHandler,
		logFn:             logFn,
		relayUserCtxKey:   userCtxKey,
		relayAPIKeyCtxKey: apiKeyCtxKey,
	}
}

func (s *RelayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Route API requests (auto-strip custom proxy/path prefixes before /api/)
	if idx := strings.Index(r.URL.Path, "/api/"); idx != -1 {
		r.URL.Path = r.URL.Path[idx:]
		s.apiHandler.ServeHTTP(w, r)
		return
	}

	// Route OpenAI/Anthropic compat API requests, v1internal endpoints, and NVIDIA pool requests.
	// NVIDIA 入口收敛到 nvidiaAliasPrefixMatch:/nvidia 与别名 /vc 共用,精确化排除 /vcard 等误吞路径(见 nvidiaPathPrefix.go)。
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1internal:") || nvidiaAliasPrefixMatch(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/responses") {
		s.compatHandler.ServeHTTP(w, r)
		return
	}

	// Only CONNECT is supported for proxy
	if r.Method != http.MethodConnect {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate proxy request
	userID, apiKeyID, err := s.authMgr.ValidateProxyAuth(r)
	if err != nil {
		s.log("Proxy auth failed: %v", err)
		w.Header().Set("Proxy-Authenticate", "Bearer")
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}

	// Inject userID into request context and forward to proxy engine
	ctx := context.WithValue(r.Context(), s.relayUserCtxKey, userID)
	if apiKeyID != "" && s.relayAPIKeyCtxKey != nil {
		ctx = context.WithValue(ctx, s.relayAPIKeyCtxKey, apiKeyID)
	}
	s.engine.ServeHTTP(w, r.WithContext(ctx))
}

// Start starts the relay server on the given port.
// If caCertPath and caKeyPath are provided and valid, the server will use TLS.
// Otherwise, it falls back to plain HTTP for backward compatibility.
func (s *RelayServer) Start(port string, caCertPath, caKeyPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return fmt.Errorf("relay server already running")
	}

	addr := "0.0.0.0:" + port

	// Try TLS if cert and key paths are provided
	var tlsConfig *tls.Config
	if caCertPath != "" && caKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(caCertPath, caKeyPath)
		if err != nil {
			s.log("⚠️ Failed to load TLS cert/key (%v), falling back to HTTP", err)
		} else {
			tlsConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
		}
	}

	var listener net.Listener
	var err error

	if tlsConfig != nil {
		listener, err = tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to listen TLS on %s: %w", addr, err)
		}
		s.isTLS = true
	} else {
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", addr, err)
		}
		s.isTLS = false
	}

	s.listener = listener
	s.trackedListener = &trackedListener{Listener: listener}
	s.server = &http.Server{
		Handler: s,
		// ReadHeaderTimeout 仅约束"收连接→读完整 header"这一阶段。既有的出站流式
		// (NVIDIA SSE 长生成 / Gemini streamGenerateContent 大于 5min)与向客户端的流式
		// 响应写出,均不在该计时器管辖范围,不会被砍——故流式长生成行为零回归。
		// 治理目标:此前 http.Server 零超时配置下,客户端/中间链路偶发只发部分 header
		// 或半死连接,会令 Accept 后的 header 读取 goroutine 永久挂起(TCP keep-alive
		// 最快 2h 才探测死连接),挤占连接/goroutine 资源。此处把该阶段封顶为 15s,
		// 超时即由 server 主动 400/关闭连接释放资源。
		// 刻意不设 ReadTimeout/WriteTimeout:前者是连接级会砍流式 body 读取与上游长生成,
		// 后者会砍 SSE 向客户端的写出,二者均为本中继链路的红线,故保留零值(不限)。
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		scheme := "http"
		if s.isTLS {
			scheme = "https"
		}
		s.log("Relay server started on %s://%s", scheme, addr)
		if err := s.server.Serve(s.trackedListener); err != nil && err != http.ErrServerClosed {
			s.log("Relay server error: %v", err)
		}
	}()

	s.isRunning = true
	return nil
}

// IsTLS returns whether the relay server is running with TLS enabled.
func (s *RelayServer) IsTLS() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isTLS
}

func (s *RelayServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	if s.server != nil {
		_ = s.server.Close()
	}
	if s.trackedListener != nil {
		s.trackedListener.CloseAll()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}

	s.isRunning = false
	s.log("Relay server stopped")
}

func (s *RelayServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isRunning
}

func (s *RelayServer) log(format string, args ...interface{}) {
	if s.logFn != nil {
		s.logFn(fmt.Sprintf("[RelayServer] "+format, args...))
	}
}

type trackedListener struct {
	net.Listener
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (tl *trackedListener) Accept() (net.Conn, error) {
	c, err := tl.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tc := &trackedConn{
		Conn: c,
		tl:   tl,
	}
	tl.mu.Lock()
	if tl.conns == nil {
		tl.conns = make(map[net.Conn]struct{})
	}
	tl.conns[tc] = struct{}{}
	tl.mu.Unlock()
	return tc, nil
}

func (tl *trackedListener) CloseAll() {
	tl.mu.Lock()
	conns := make([]net.Conn, 0, len(tl.conns))
	for c := range tl.conns {
		conns = append(conns, c)
	}
	tl.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

type trackedConn struct {
	net.Conn
	tl *trackedListener
}

func (tc *trackedConn) Close() error {
	tc.tl.mu.Lock()
	if tc.tl.conns != nil {
		delete(tc.tl.conns, tc)
	}
	tc.tl.mu.Unlock()
	return tc.Conn.Close()
}
