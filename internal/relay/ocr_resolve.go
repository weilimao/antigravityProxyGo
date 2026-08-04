package relay

// ocr_resolve.go —— L1 入口的图片源解析:base64 / Data URL / 网络 URL 三态归一到 (b64, mime)。
//
// 三层分离架构的 L1 入口前置:协议适配层(L2)从各协议的 image 块里拿到"原始图片引用"
// (可能是纯 base64、data: 前缀的 Data URL、或 http(s) 网络地址),统一交本文件解析为
// (b64, mime) 后再送 OCR 引擎。这样:
//   - 三种协议(Anthropic source / Gemini inlineData / OpenAI image_url)的 URL 处理只有一份实现;
//   - SSRF 防护、urlCache 命中/下载、Data URL 解析的逻辑集中可测。
//
// 两条路径:
//   - resolveB64NoFetch:仅查缓存,绝不下载。供"窗外历史图" / "只查缓存"链路,
//     URL 图命中 urlCache 才复用,未命中返回 ok=false → 调用方走占位文本(省下载+省配额)。
//   - resolveB64WithFetch:可下载。供"窗内图" miss 真打链路,URL 图命中 urlCache 复用 b64,
//     未命中走 fetchImageAsBase64(SSRF 防护)下载后回填 urlCache 再送 OCR。

import "strings"

// resolveB64NoFetch 仅从缓存解析图片为 (b64, mime),绝不触达网络下载。
//
// 入参 src 为 Anthropic image source(Type / Data / Url / MediaType)。
// 返回 (b64, mime, ok):ok=true 表示可送 OCR/缓存复用;ok=false 表示 URL 图尚未下载过
// (窗外场景调用方应走占位文本兜底)。
//
// 分流:
//   - Type=="base64" 且 Data 非空 → 直接返回 Data(做 data: 前缀兜底解析);
//   - Data 以 data: 开头 → parseDataURL;
//   - Type=="url" 或 Data 以 http(s) 开头 → 取真实 URL(优先 Url 字段),仅查 urlCache;
//   - 其余 → ok=false(空数据或不可识别)。
func (s *OCRService) resolveB64NoFetch(src *AnthropicImageSource) (b64, mime string, ok bool) {
	if s == nil || src == nil {
		return "", "", false
	}
	// 优先识别 URL 形态(无论 Type 标的是 url 还是 base64,只要数据像 URL 就按 URL 处理,
	// 兼容把 URL 误塞进 Data 字段的客户端)。
	urlStr := strings.TrimSpace(src.Url)
	if urlStr == "" && looksLikeHTTPURL(src.Data) {
		urlStr = strings.TrimSpace(src.Data)
	}
	if urlStr != "" {
		if s.urlCache != nil {
			if b, m, hit := s.urlCache.get(urlCacheKey(urlStr)); hit {
				if mime := strings.TrimSpace(src.MediaType); mime != "" {
					return b, mime, true
				}
				return b, m, true
			}
		}
		return "", "", false
	}
	// Data URL(data:image/..;base64,..):带头部前缀,解析出标准 b64。
	if strings.HasPrefix(strings.TrimSpace(src.Data), "data:") {
		if b, m, hit := parseDataURL(src.Data); hit {
			if mime := strings.TrimSpace(src.MediaType); mime != "" {
				return b, mime, true
			}
			return b, m, true
		}
		return "", "", false
	}
	// 纯 base64:直接用(原 P1 路径)。
	if strings.TrimSpace(src.Data) != "" {
		mime := src.MediaType
		if mime == "" {
			mime = "image/jpeg"
		}
		return src.Data, mime, true
	}
	return "", "", false
}

// resolveB64WithFetch 解析图片为 (b64, mime),URL 图允许下载(SSRF 防护下)并回填 urlCache。
//
// 与 resolveB64NoFetch 的差异仅在 URL 分支:命中 urlCache 直接复用;未命中走
// fetchImageAsBase64 下载,成功后回填 urlCache,再返回 b64 供 OCR。
// data URL / 纯 base64 分支与 NoFetch 完全一致。
func (s *OCRService) resolveB64WithFetch(src *AnthropicImageSource) (b64, mime string, err error) {
	if s == nil || src == nil {
		return "", "", errSSRFRejected
	}
	urlStr := strings.TrimSpace(src.Url)
	if urlStr == "" && looksLikeHTTPURL(src.Data) {
		urlStr = strings.TrimSpace(src.Data)
	}
	if urlStr != "" {
		// 先查 urlCache,命中免下载。
		if s.urlCache != nil {
			if b, m, hit := s.urlCache.get(urlCacheKey(urlStr)); hit {
				if mime := strings.TrimSpace(src.MediaType); mime != "" {
					return b, mime, nil
				}
				return b, m, nil
			}
		}
		// 未命中 → 下载(SSRF 防护内建在 fetchImageAsBase64)。
		b, m, ferr := fetchImageAsBase64(urlStr)
		if ferr != nil {
			return "", "", ferr
		}
		// 回填 urlCache(超大图 set 内部会自跳过,防内存放大)。
		if s.urlCache != nil {
			s.urlCache.set(urlCacheKey(urlStr), b, m)
		}
		if mime := strings.TrimSpace(src.MediaType); mime != "" {
			return b, mime, nil
		}
		return b, m, nil
	}
	// Data URL。
	if strings.HasPrefix(strings.TrimSpace(src.Data), "data:") {
		if b, m, hit := parseDataURL(src.Data); hit {
			if mime := strings.TrimSpace(src.MediaType); mime != "" {
				return b, mime, nil
			}
			return b, m, nil
		}
		return "", "", errSSRFRejected
	}
	// 纯 base64。
	if strings.TrimSpace(src.Data) != "" {
		mime := src.MediaType
		if mime == "" {
			mime = "image/jpeg"
		}
		return src.Data, mime, nil
	}
	return "", "", errSSRFRejected
}
