package proxy

// handler_remote.go: ProxyHandler 远程中继转发链路 —— 客户端模式下把本地 IDE 请求经 TLS 中继隧道转发至远端。
// 从 handler.go 按职责拆分而出(同包内共享符号,物理搬移,逻辑逐行等价,零回归):
//   getRemoteClient       动态获取/复用具备远程中继拨号链路的 http.Client 单例
//   ResetRemoteClient     断开或切换远程中继后清理旧 Transport 与连接池
//   forwardThroughRemote  在 TLS 层上把请求中继转发至远端服务器,执行流式转发与抓包落库
//   getGoogleChannel      谷歌渠道通道名选择(切到 nvidia 选项卡时回退 antigravity/project)
// 注意:ServeHTTP 主路由分发与 ProxyHandler 结构体定义仍保留在 handler.go。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/stats"
)

// getRemoteClient 动态获取并复用具备远程中继拨号链路的全局 http.Client 单例
func (h *ProxyHandler) getRemoteClient() *http.Client {
	h.remoteClientMu.Lock()
	defer h.remoteClientMu.Unlock()

	if h.remoteClient != nil {
		return h.remoteClient
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if currentRr := h.getRemoteRelay(); currentRr != nil && currentRr.IsConnected() {
				return currentRr.DialThroughRemote(addr)
			}
			return nil, errors.New("remote relay disconnected")
		},
		TLSClientConfig:       getRemoteTLSConfig(""),
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       300 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	h.remoteClient = &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute,
	}
	return h.remoteClient
}

// ResetRemoteClient 清理并重置远程中继 HTTP Client 单例。
// 在 Disconnect 或切换远程中继后调用，确保旧的 Transport（含旧 TLS 配置和连接池）被释放。
func (h *ProxyHandler) ResetRemoteClient() {
	h.remoteClientMu.Lock()
	defer h.remoteClientMu.Unlock()
	if h.remoteClient != nil {
		if t, ok := h.remoteClient.Transport.(*http.Transport); ok {
			t.CloseIdleConnections()
		}
		h.remoteClient = nil
	}
}

// forwardThroughRemote 处理客户端模式下的 HTTP 请求路由，将请求在 TLS 层上中继至远端服务器并执行流式转发与抓包
func (h *ProxyHandler) forwardThroughRemote(w http.ResponseWriter, r *http.Request, bodyBytes []byte, targetHost, targetPath string, rr RemoteRelayInterface) {
	startTime := time.Now()
	relayAPIKeyID, _ := r.Context().Value(RelayAPIKeyCtxKey).(string)
	if relayAPIKeyID == "" {
		relayAPIKeyID = r.Header.Get("X-Relay-Api-Key-Id")
	}
	logPrefix := fmt.Sprintf("[RemoteForward][%s -> %s%s]", r.Method, targetHost, r.URL.Path)
	if h.logFn != nil {
		h.logFn(fmt.Sprintf("%s 🌐 正在将本地 IDE 请求中继转发至远程服务器...", logPrefix))
	}

	// 1. 构造发往公网目标的 HTTPS 请求，使得中继服务器接收时能执行 MITM 解密
	targetUrl := "https://" + targetHost + targetPath
	timeoutSec := 300
	if h.getRequestTimeout != nil {
		if val := h.getRequestTimeout(); val > 0 {
			timeoutSec = val
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	proxyReq, errReq := http.NewRequestWithContext(ctx, r.Method, targetUrl, bytes.NewReader(bodyBytes))
	if errReq != nil {
		h.logFn(fmt.Sprintf("❌ Failed to create remote forward request: %v", errReq))
		http.Error(w, errReq.Error(), http.StatusInternalServerError)
		return
	}

	// 2. 复制原始请求头
	for k, values := range r.Header {
		proxyReq.Header[k] = values
	}
	proxyReq.Header.Set("Host", targetHost)

	// Generate and set unique request ID for Option B async logging
	reqID := fmt.Sprintf("rl_%d", time.Now().UnixNano())
	proxyReq.Header.Set("X-Antigravity-Req-ID", reqID)

	// 3. 使用全局复用的中继 Client 发送请求
	client := h.getRemoteClient()
	resp, errDo := client.Do(proxyReq)
	if errDo != nil {
		h.logFn(fmt.Sprintf("❌ Remote relay forward Do failed: %v", errDo))
		http.Error(w, errDo.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 4. 将响应头及状态码写回 IDE 客户端
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// 5. 转发响应体并捕获用于本地抓包记录
	flusher, isFlusher := w.(http.Flusher)
	buf := make([]byte, 4096)
	var respBodyBuf bytes.Buffer

	// 监听 Context 取消事件，一旦取消立即关闭上游响应体，强行中断阻塞的 Read
	cancelChan := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = resp.Body.Close()
		case <-cancelChan:
		}
	}()
	defer close(cancelChan)

	for {
		n, errRead := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, errWrite := w.Write(chunk)
			if errWrite != nil {
				h.logFn(fmt.Sprintf("⚠️ Failed to write response to client: %v", errWrite))
				break
			}
			if isFlusher {
				flusher.Flush()
			}
			// 仅在数据量较小时记录响应体，降低并发内存占用
			if respBodyBuf.Len() < 512*1024 {
				respBodyBuf.Write(chunk)
			}
		}
		if errRead != nil {
			if errRead != io.EOF {
				h.logFn(fmt.Sprintf("⚠️ Error reading response from remote: %v", errRead))
			}
			break
		}
	}

	// 6. 保存至本地数据包记录器，使前端的“拦截历史”依然能抓包展示
	if h.packetCap != nil {
		h.packetCap.SavePacket(r.Method, targetHost, targetPath, r.Header, bodyBytes, resp.Header, respBodyBuf.Bytes(), resp.StatusCode)
	}

	if h.logFn != nil {
		h.logFn(fmt.Sprintf("%s ✅ 远程中继转发完成，状态码: %d", logPrefix, resp.StatusCode))
	}

	// 7. 直接在客户端本地计算并保存请求日志，不再网络中继拉取服务端日志
	inTokens, outTokens, cachedTokens := 0, 0, 0
	currentModel := "unknown"
	modelMatch := reModelInPath.FindStringSubmatch(targetPath)
	if len(modelMatch) > 1 {
		currentModel = modelMatch[1]
	} else if strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		currentModel = "antigravity-core"
		if len(bodyBytes) > 0 {
			var bodyJson struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(bodyBytes, &bodyJson) == nil && bodyJson.Model != "" {
				currentModel = bodyJson.Model
			}
		}
	}

	if resp.StatusCode == 200 && strings.Contains(strings.ToLower(targetPath), "generatecontent") {
		bodyStr := string(decompressIfNeeded(respBodyBuf.Bytes(), resp.Header))
		pm := rePromptTokens.FindAllStringSubmatch(bodyStr, -1)
		cm := reCandidateTokens.FindAllStringSubmatch(bodyStr, -1)
		cc := reCachedTokens.FindAllStringSubmatch(bodyStr, -1)

		if len(pm) > 0 && len(pm[len(pm)-1]) > 1 {
			inTokens, _ = strconv.Atoi(pm[len(pm)-1][1])
		}
		if len(cm) > 0 && len(cm[len(cm)-1]) > 1 {
			outTokens, _ = strconv.Atoi(cm[len(cm)-1][1])
		}
		if len(cc) > 0 && len(cc[len(cc)-1]) > 1 {
			cachedTokens, _ = strconv.Atoi(cc[len(cc)-1][1])
		}
	}

	isRealModel := strings.Contains(strings.ToLower(r.URL.Path), "generatecontent") || strings.Contains(strings.ToLower(r.URL.Path), "predict")
	if isRealModel && currentModel != "" && currentModel != "unknown" {
		rate := h.statsTracker.GetPricingMgr().GetPricingForModel(currentModel)
		nonCachedIn := inTokens - cachedTokens
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
		inputCost := math.Round((float64(nonCachedIn)*rate.Input/1000000.0)*1000000.0) / 1000000.0
		outputCost := math.Round((float64(outTokens)*rate.Output/1000000.0)*1000000.0) / 1000000.0
		cachedCost := math.Round((float64(cachedTokens)*rate.Cached/1000000.0)*1000000.0) / 1000000.0
		totalCost := inputCost + outputCost + cachedCost

		logMethod := r.Method
		if m := r.Header.Get("X-Antigravity-Original-Method"); m != "" {
			logMethod = m
		}
		logPath := r.URL.Path
		if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
			logPath = p
		}
		sessionID := "remote_session"
		if p := r.Header.Get("X-Antigravity-Original-Path"); p != "" {
			sessionID = "compat-api"
		}

		dbItem := &db.RequestLog{
			ReqID:        reqID,
			Timestamp:    time.Now().Format(time.RFC3339),
			Mode:         "remote",
			UserID:       rr.GetConfig().UserKey,
			ModelName:    currentModel,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Cost:         totalCost,
			InputCost:    inputCost,
			OutputCost:   outputCost,
			CachedCost:   cachedCost,
			DurationMs:   time.Since(startTime).Milliseconds(),
			StatusCode:   resp.StatusCode,
			Method:       logMethod,
			Host:         targetHost,
			Path:         logPath,
			SessionID:    sessionID,
		}
		_ = db.InsertRequestLog(dbItem)

		var reqBodyParsed interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &reqBodyParsed); err != nil {
				reqBodyParsed = string(bodyBytes)
			}
		}

		headersMap := make(map[string]interface{})
		for k, v := range r.Header {
			if len(v) > 0 {
				headersMap[k] = v[0]
			}
		}

		// Record locally in memory tracker so it shows up on the client dashboard
		h.statsTracker.AddRequestLog(&stats.RequestLog{
			ID:             reqID,
			Timestamp:      time.Now().Format("01/02 15:04:05"),
			Method:         logMethod,
			Host:           targetHost,
			Path:           logPath,
			Model:          currentModel,
			Account:        rr.GetConfig().UserKey,
			InTokens:       inTokens,
			OutTokens:      outTokens,
			CachedTokens:   cachedTokens,
			Cost:           totalCost,
			StatusCode:     resp.StatusCode,
			RequestBody:    reqBodyParsed,
			RequestHeaders: headersMap,
			SessionID:      sessionID,
			DurationMs:     time.Since(startTime).Milliseconds(),
		})

		// Record usage locally so the client UI can reflect the remote quota consumption
		h.usageTracker.RecordUsage(stats.UsageSample{
			ModelName:    currentModel,
			InTokens:     inTokens,
			OutTokens:    outTokens,
			CachedTokens: cachedTokens,
			Account:      nil, // This will map to "direct" which is used for global totals like remote quotas
		})

		// 触发 Relay Quota 扣减（如果是 Relay 下游用户请求）
		relayUserID, _ := r.Context().Value(RelayUserCtxKey).(string)
		if relayUserID != "" && h.relayStatsCallback != nil {
			h.relayStatsCallback("远程中继", relayUserID, relayAPIKeyID, currentModel, inTokens, outTokens, cachedTokens,
				r.Method, targetHost, targetPath, "remote_session", time.Since(startTime).Milliseconds(), resp.StatusCode, reqID)
		}
	}
}

// getGoogleChannel 返回用于谷歌 Cloud AI / Antigravity 渠道请求（18443）的有效通道名称。
// 当前端 UI 当前处于 "nvidia" 选项卡时，自动回退到 "antigravity"（或 "project"），
// 确保本地 CLI (agy) 和扩展发往 Google 的请求绝不会误用 NVIDIA 账号。
func (h *ProxyHandler) getGoogleChannel() string {
	ch := h.accountMgr.GetActiveChannel()
	if ch == "nvidia" {
		if h.accountMgr.GetProjectPoolMode() {
			return "project"
		}
		return "antigravity"
	}
	return ch
}
