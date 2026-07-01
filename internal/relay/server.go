package relay

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
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
	// Route API requests
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.apiHandler.ServeHTTP(w, r)
		return
	}

	// Route OpenAI/Anthropic compat API requests
	if strings.HasPrefix(r.URL.Path, "/v1/") {
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

func (s *RelayServer) Start(port string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return fmt.Errorf("relay server already running")
	}

	addr := "0.0.0.0:" + port
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = listener
	s.trackedListener = &trackedListener{Listener: listener}
	s.server = &http.Server{
		Handler: s,
	}

	go func() {
		s.log("Relay server started on %s", addr)
		if err := s.server.Serve(s.trackedListener); err != nil && err != http.ErrServerClosed {
			s.log("Relay server error: %v", err)
		}
	}()

	s.isRunning = true
	return nil
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
