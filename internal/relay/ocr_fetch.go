package relay

// ocr_fetch.go —— URL 图片抓取(含 SSRF 防护)。
//
// 三层分离架构的 L1 入口前置:当图片以网络 URL(http/https)形式传入时,先抓取字节转 base64,
// 再交 L1 引擎 OCR。本文件承载 SSRF 防护与下载稳健性:
//   - scheme 仅限 http/https;
//   - DialContext 在拨号前解析目标 IP,拒绝回环 / 私网(RFC1918) / 链路本地(169.254) /
//     唯一本地地址(fc00::/7) 等非公网目标,杜绝中继被当作 SSRF 跳板探测内网/云元数据;
//   - 禁跟随重定向(CheckRedirect 返回 http.ErrUseLastResponse),避免 30x 跳转绕过 IP 校验;
//   - 5 秒超时,io.LimitReader 限定 10MB,防恶意大图拖垮内存;
//   - 响应 Content-Type 必须 image/*,拒绝把 HTML/JSON 文本当图回显给上游模型形成 SSRF 读信道。
//   - 跳过系统出站代理(netutil.NewClient 会注入,会破坏"目标 IP 即用户给的 URL"语义),
//     用独立 transport 直连校验。
//
// 下载成功后返回的标准 base64 会被 L1 用于:(a) 喂给 gemini OCR;(b) 入 ocrCache(b64→text)。
// 同时 urlCache(url→b64)由 L1 入口维护,本轮窗外相同 URL 图可跳过下载直接命中 b64。

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ocrFetchMaxBytes 限制单张下载图最大字节数(10MB),防恶意大图拖垮内存。
const ocrFetchMaxBytes = 10 << 20

// ocrFetchTimeout 是单次下载的整体超时(含拨号 + 读体),5 秒兼顾稳定性与体感。
const ocrFetchTimeout = 5 * time.Second

// ssrfRejectedPrefix 用于区分"拒绝(SSRF/非 image)"与"网络错误"两类失败,供日志区分。
var errSSRFRejected = errors.New("ocr fetch: target rejected by SSRF guard")
var errNotImage = errors.New("ocr fetch: response content-type is not image/*")

// ssrfLoopbackAllowed 是仅供测试使用的开关:为 true 时 SSRF 拨号守卫放行回环/私网地址,
// 让 httptest(绑 127.0.0.1)图床可被下载,从而能对下载链路做端到端单测。
// 生产代码绝不置 true(默认 false,SSRF 守卫全生效)。用 atomic 避免数据竞争。
// 注:严谨做法本应是依赖注入 dialer,但本函数被 http.Transport.DialContext 引用需保持
// 闭包签名简单;以进程级测试开关放宽守卫,并在开关置位期间通过 httptest 验证下游链路,
// 完成后立即还原,属可接受的取舍(详见 ocr_fetch_test.go 的 t.Cleanup 还原)。
var ssrfLoopbackAllowed atomic.Bool

// ssrfDialer 在拨号前解析目标 IP 并拒绝非公网地址。
// 逐条检查所有解析出的 IP,任一为私网/回环/链路本地/保留即拒绝(防 happy-eyeballs 一路公网一路私网绕过)。
func ssrfGuardDialCtx(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !ssrfLoopbackAllowed.Load() && !isPublicIP(ip) {
			return nil, fmt.Errorf("%w: %s resolves to non-public %s", errSSRFRejected, host, ip)
		}
	}
	d := &net.Dialer{Timeout: ocrFetchTimeout}
	return d.DialContext(ctx, network, net.JoinHostPort(host, port))
}

// isPublicIP 判定一个 IP 是否公网可达。拒绝回环 / 私网 / 链路本地 / 唯一本地 / 未指定 / 保留段。
// 对 IPv4 映射的 IPv6(::ffff:a.b.c.d)其 To4() 会返回.ipv4 形式,统一按 v4 校验。
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// 额外拒绝 fc00::/7(ULA)与 2001:db8/32(文档预留),Go 标准库未覆盖。
	if v4 := ip.To4(); v4 == nil {
		// IPv6
		if len(ip) == 16 {
			// fc00::/7 → 前字节 0xfc 或 0xfd
			if ip[0] == 0xfc || ip[0] == 0xfd {
				return false
			}
			// 2001:db8::/32
			if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
				return false
			}
		}
	}
	return true
}

// fetchImageAsBase64 下载一张网络图片并转标准 base64。
// 返回 (b64Std, mime, err):成功 mime 形如 "image/png";失败 err 区分 SSRF / 非 image / 网络/超时。
// 调用方拿到 b64 后再交 L1 OCR 引擎识别。
func fetchImageAsBase64(rawURL string) (b64Std, mime string, err error) {
	u, perr := url.Parse(strings.TrimSpace(rawURL))
	if perr != nil {
		return "", "", fmt.Errorf("parse image url: %w", perr)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", fmt.Errorf("%w: scheme %s not allowed (only http/https)", errSSRFRejected, scheme)
	}

	// 独立 transport:跳过系统代理(避免代理破坏 IP 语义),DialContext 走 SSRF 拦截,
	// 禁跟随重定向(防 30x 绕过)。
	transport := &http.Transport{
		DialContext:     ssrfGuardDialCtx,
		Proxy:           nil,
		MaxConnsPerHost: 2,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   ocrFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", fmt.Errorf("create image fetch request: %w", err)
	}
	req.Header.Set("User-Agent", "antigravity-proxy/ocr-fetch")
	req.Header.Set("Accept", "image/*")

	resp, err := client.Do(req)
	if err != nil {
		// 区分 SSRF 拒绝与普通网络错误,供调用方日志约定。
		if errors.Is(err, errSSRFRejected) {
			return "", "", err
		}
		return "", "", fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("fetch image: status %d", resp.StatusCode)
	}

	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	mime = ct
	if idx := strings.Index(ct, ";"); idx > 0 {
		mime = strings.TrimSpace(ct[:idx])
	}
	if !strings.HasPrefix(strings.ToLower(mime), "image/") {
		return "", "", fmt.Errorf("%w: %s", errNotImage, ct)
	}

	// LimitReader 截断超限图,ReadAll 实际读到的字节数 < cap。
	limited := io.LimitReader(resp.Body, ocrFetchMaxBytes+1)
	body, rerr := io.ReadAll(limited)
	if rerr != nil {
		return "", "", fmt.Errorf("read image body: %w", rerr)
	}
	if len(body) > ocrFetchMaxBytes {
		return "", "", fmt.Errorf("fetch image: exceeds %d bytes", ocrFetchMaxBytes)
	}
	return base64.StdEncoding.EncodeToString(body), mime, nil
}
