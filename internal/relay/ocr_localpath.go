package relay

// ocr_localpath.go —— L2.5 预处理层:入站 text 块裸本地图片路径 → 自动读图 OCR 注入。
//
// 背景:Claude Code 等客户端按客户端侧 model ID 匹配内置已知模式判定 image 支持
// (见 memory claude-code-image-input-client-id-based)。自定义 provider 前缀名
// (如 nvidia/z-ai/glm-4.9,不含 claude/anthropic 关键字)被判定为"不支持 image inputs",
// 客户端在发送前就把 image 块剔除,本地截图路径作为纯 Type=="text" 字符串发到网关。
// 现有 L2 image 降级(ocr_downgrade_*.go)只扫结构化 Type=="image" / InlineData /
// image_url 块,收不到图、永不触发。
//
// 本层在 L2 image 降级闸**之前**做"text 块裸路径预扫":识别"本机图片绝对路径"候选,
// 仅当本地确实存在该文件时读字节→base64→复用 L1 OCRService.OcrImage(共享现有
// LRU+SQLite 两级缓存 + singleflight)→把识别文本经 nvidiaImageOcrDescHeader 包装后
// 原地改写 text 块,再交下游正常转发。本地不存在/读不到/非图/超大/OCR 失败 → 保持
// text 原样,不报错(静默 miss)。适配 Windows(盘符 D:\)与 Mac(Unix 绝对路径 /)。
//
// 安全:本功能走 os.ReadFile 独立通路,**不碰** ocr_fetch.go 的 SSRF scheme 白名单
// (http/https only,file:// /裸磁盘路径被 errSSRFRejected 拒绝的 LFI 硬防线不动)。
// 读本地文件能力受 isLocalDirectSession 闸(request 来源判定)守住:仅本地自用直连
// (official_bypass / default_bypass)放行,远程外部中继用户(后台持久化 API Key /
// web 登录态)天然堵死,无需任何设置开关。详见 ocr_localpath_test.go 的来源矩阵。
//
// 复用 L1 缓存防重复分析:同图同会话跨提问命中(image-only 三维缓存键),与现有
// image 块降级路径共享同一缓存池(同 b64→同 key→全局每图一次)。OCR 失败也走 L1 的
// 失败短 TTL(30s)熔断,短期内同名文件不重打上游,与现有机制一致。

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// localPathImageMaxBytes 限制单张本地图片最大字节数(10MB),对齐 ocr_fetch.go:34
// ocrFetchMaxBytes。防把本地大文件读入内存放大占用。
const localPathImageMaxBytes = 10 << 20

// localImageExts 是被认可为"本机图片绝对路径"的扩展名集合(小写)。
// 与 ocr_capability.go / 各降级层覆盖的图片格式一致。
var localImageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".tif":  true,
	".tiff": true,
	".svg":  true,
}

// extByMime 是 http.DetectContentType 嗅探不出 image/* 时,按扩展名补 mime 的兜底表
// (DetectContentType 对 webp/tiff/svg 等较新或矢量格式常识别为 application/octet-stream)。
var extByMime = map[string]string{
	".webp": "image/webp",
	".tiff": "image/tiff",
	".tif":  "image/tiff",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
}

// localPathHead 抓"绝对路径头 + 贪婪非分隔字符段"的候选 token。
//
// 两段式识别(详见方案 §1):本正则只抓"可能含本地图片路径的候选段",终端校验
// (末尾须恰好落在图片扩展名)交 truncateToImageExt 在 Go 层从右往左回溯完成,
// 避免纯正则在"紧贴中文"(图D:\x\a.png看)与"多点位"等场景下的歧义。
//
// 左边界:[A-Za-z]:[\\/] (Windows 盘符) 或 前导 / (Unix 绝对路径)。
//   - Windows 反斜杠与 Unix 正斜杠在 path class 都覆盖;os.Stat/ReadFile 在 Windows
//     自动接受正反斜杠,无需 filepath.ToSlash。
//   - "打开D盘img.png文件" 的 "D盘" 不满足冒号后需 \ 或 /,本正则不匹配 → 放弃。
//
// 主体:[^\s"'<>|*?]* 贪婪吃到最后一个非(空白/引号/尖括号/竖线/通配符)的字符,
// CJK 汉字全吞(含紧贴中文)。这是"右边界放宽"的关键:不再要求路径后接分隔符,
// 紧贴中文也算命中,靠终端校验截掉尾随非扩展名字符(如 '看')。
//
// 不含扩展名约束:扩展名校验交给 truncateToImageExt,使 "D:\a\b.cs"(无图片扩展名)
// 在终端校验时被丢弃,而 "b.png"(无盘符/前导 /)因左边界不匹配直接放弃。
var localPathHead = regexp.MustCompile(`[A-Za-z]:[\\/][^\s"'<>|*?]*|/[^\s"'<>|*?]*`)

// imagePathMatch 是一次匹配的产物:path 为终端校验后截到的净图片绝对路径
// (供 os.Stat),surroundingTail 为路径 token 在原文本里紧随其后、应被保留的
// 尾随字符(如 '看'、' 是图' 之类,truncate 吃掉的是路径 token 自身尾部的非扩展名
// 段,而非 token 之后的文本)。start/end 为 token 在原文本里的字节区间。
type imagePathMatch struct {
	path            string
	start, end      int
	surroundingTail string
}

// findLocalImagePathTokens 扫一段文本,返回所有"本机图片绝对路径"候选。
//
// 实现(方案 §1 两段式):
//  1. localPathHead 贪婪抓所有"绝对路径头 + 非分隔字符段"候选;
//  2. 对每段候选调 truncateToImageExt 从右往左找最后一个图片扩展名落点,命中则把
//     候选截断到该扩展名末尾(吃掉前缀有效路径 + 扩展名),丢弃尾随的非扩展名 CJK 等;
//     无任何图片扩展名后缀 → 丢弃该候选;
//  3. 截断后的 path 供 os.Stat;原 token 区间 [start,end) 记录其与紧随其后的
//     surroundingTail 一起,供 resolveLocalImagePathsInText 原地改写时保留周围文字。
func findLocalImagePathTokens(text string) []imagePathMatch {
	var matches []imagePathMatch
	for _, loc := range localPathHead.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		candidate := text[start:end]
		path, trimmed := truncateToImageExt(candidate)
		if path == "" {
			continue
		}
		matches = append(matches, imagePathMatch{
			path:            path,
			start:           start,
			end:             start + len(trimmed),
			surroundingTail: text[start+len(trimmed) : end],
		})
	}
	return matches
}

// truncateToImageExt 对一段候选串从右往左找最后一个 '.' 点位,检查其后缀是否 ∈ 图片
// 扩展名集(大小写不敏感)。命中则返回 (净路径=候选[:点位+扩展名长度], 截断段=同左)
// —— 即吃掉候选里该扩展名之后所有尾随字符(如 '看');无任何图片扩展名后缀 → ("", "")。
//
// 例:
//   - "D:\x\a.png看" → 最后一个 '.' 在 idx,len-3 处 ".png" ∈ 集合 → 返回 ("D:\x\a.png", "D:\x\a.png"),吃掉 '看';
//   - "D:\a\b.cs"    → '.cs' 不在集合 → 继续往左找其它 '.',无图片扩展名 → 返回 ("", "");
//   - "D:\x.a\a.png看" → 从右第一个 '.png' 命中 → 返回 ("D:\x.a\a.png", ...),中间的 '.a' 不干扰。
//
// 多点位正确性:从右往左逐个 '.' 检查,首个命中的图片扩展名即为终点(最右的图片扩展名),
// 前面的 '.' 视作路径段的一部分(如 D:\x.a\a.png 的 '.a')。
func truncateToImageExt(candidate string) (path string, trimmed string) {
	// 从右往左找每一个 '.' 点位。
	for i := len(candidate) - 1; i >= 0; i-- {
		if candidate[i] != '.' {
			continue
		}
		// candidate[i:] 形如 ".png看" / ".cs" / ".a";取到下一个非字母数字字符为止作为扩展名候选。
		// 扩展名本身只含字母( png/jpg/webp...),遇到非字母即扩展名结束;其后的是尾随非扩展名字符。
		extEnd := i + 1
		for extEnd < len(candidate) {
			c := candidate[extEnd]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				break
			}
			extEnd++
		}
		ext := strings.ToLower(candidate[i:extEnd])
		if localImageExts[ext] {
			return candidate[:extEnd], candidate[:extEnd]
		}
		// 当前 '.' 后的扩展名不是图片扩展名,继续往左找下一个 '.'(处理多点位)。
	}
	return "", ""
}

// resolveLocalImageToB64 读本地图片文件为标准 base64 + 真实 mime。
//
// 守卫链(任一不过即返回 ("", "", false),调用方保持 text 原样静默 miss):
//  1. os.Stat:文件存在且非目录;
//  2. size ≤ localPathImageMaxBytes(10MB,对齐 ocrFetchMaxBytes);
//  3. os.ReadFile 读字节;
//  4. http.DetectContentType(首 512 字节)嗅探真实 mime,须 image/*;DetectContentType
//     识别不出的图片格式(webp/tiff/svg/bmp)按扩展名查 extByMime 补 mime;
//  5. base64.StdEncoding 编码。
//
// 与 ocr_fetch.go 的 SSRF 下载通路完全独立:这里不触网,只读本机磁盘,受调用方
// isLocalDirectSession 闸 + 文件存在 + size + 真实 mime 四重守卫。
func resolveLocalImageToB64(path string) (b64, mime string, ok bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", "", false
	}
	if info.Size() > localPathImageMaxBytes {
		return "", "", false
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
		return "", "", false
	}
	detected := http.DetectContentType(body)
	if !strings.HasPrefix(strings.ToLower(detected), "image/") {
		// DetectContentType 对 webp/tiff/svg/bmp 常识别为 application/octet-stream,
		// 按扩展名补 mime 兜底(防这些格式被误判非图而静默 miss)。
		lower := strings.ToLower(path)
		if m, hit := extByMime[getExt(lower)]; hit {
			detected = m
		} else {
			return "", "", false
		}
	}
	return base64.StdEncoding.EncodeToString(body), detected, true
}

// getExt 取路径最后一段的扩展名(含点,小写)。无扩展名返回空串。
func getExt(lowerPath string) string {
	for i := len(lowerPath) - 1; i >= 0; i-- {
		if lowerPath[i] == '.' {
			return lowerPath[i:]
		}
		if lowerPath[i] == '/' || lowerPath[i] == '\\' {
			break
		}
	}
	return ""
}

// isLocalDirectSession 判定请求是否来自"本地自用直连",决定是否放行本功能。
//
// 判据(方案 §5,比 UserID==default_local_admin 更普适):RelaySession.APIKeyID:
//   - APIKeyIDOfficialBypass / APIKeyIDDefaultBypass(官方 key 前缀 sk-ant-/nvapi-/sk-
//     兜底分支):本地 IDE 拿官方 key 直连 18444 的指纹 → 放行;
//   - 真实 key.ID(后台持久化 API Key):外部中继用户在面板创建的 API Key → 拒绝(LFI 堵死);
//   - 空串(web Login session):远程管理员后台登录态 → 拒绝(保守,即便误判也偏安全);
//   - nil session → 拒绝(防御)。
//
// 本闸比 UserID==default_local_admin 何时更优:本地自用且在面板建了启用中继用户(如为
// 统计)时,UserID 变成真实 ID 但 APIKeyID 仍是 official_bypass,本判据仍放行;反之
// UserID==default_local_admin 会在该场景误关。
func (s *OCRService) isLocalDirectSession(userSession *RelaySession) bool {
	if userSession == nil {
		return false
	}
	return userSession.APIKeyID == APIKeyIDOfficialBypass || userSession.APIKeyID == APIKeyIDDefaultBypass
}

// resolveLocalImagePathsInText 是三协议共用的纯文本核心:扫 text → 命中本地图片路径 →
// 读文件 → base64 → L1 OcrImage → nvidiaImageOcrDescHeader 包装 → 原地改写 text。
//
// 返回 (newText, replaced):
//   - newText:改写后的完整文本。无路径命中或全部静默 miss 时 == text(零变更);
//   - replaced:实际成功注入 OCR 文本的路径数(供调用方日志)。
//
// 改写语义(方案 §2):用 strings.Builder 一次性重建,把"非匹配段 + 已改写匹配段"按原序
// 拼回,避免多路径 offset 漂移。保留周围文字(含被 truncate 吃掉的尾随 CJK——只把路径
// token 替换成 descHeader,token 后的 '看'、' 是图' 等尾随字保留)。
//
// 失败路径(文件不存在/非图/超大/OCR 报错/空文本):保持 text 原样,不插占位、不返回 error
// 阻断。OCR 失败时 L1 会写一条失败短 TTL(30s)缓存,短期内同名文件仍命中失败条目→直接
// 复用"失败"态不重打上游(防反复失败重试打爆号池),与现有机制一致。
//
// 复用 L1 缓存防重复分析:调 s.OcrImage 即自动享 LRU+SQLite 两级缓存 + singleflight
// (image-only 三维键),同图同会话跨提问命中零重打上游,与现有 image 块降级共享同一缓存池。
func (s *OCRService) resolveLocalImagePathsInText(text string, userSession *RelaySession) (newText string, replaced int) {
	if s == nil || strings.TrimSpace(text) == "" {
		return text, 0
	}
	// 入口闸:仅本地自用直连放行,远程外部中继用户静默跳过(LFI 堵死,无需设置开关)。
	if !s.isLocalDirectSession(userSession) {
		return text, 0
	}
	matches := findLocalImagePathTokens(text)
	if len(matches) == 0 {
		return text, 0
	}
	ocrModel := s.getOcrModel()

	// 收集本 text 内所有非路径文本段作为 OCR 靶向上下文(promptCtx 不参与缓存键 image-only,
	// 仅 miss 真打上游时组 ocrPrompt 用),与现有降级层 userPromptCtx 口径一致。
	var promptCtxBuilder strings.Builder
	for _, m := range matches {
		// 把去掉路径 token 的其余文本拼起来(粗粒度上下文,不含路径串本身)。
		if promptCtxBuilder.Len() > 0 {
			promptCtxBuilder.WriteString("\n")
		}
		promptCtxBuilder.WriteString(strings.TrimSpace(text[:m.start]))
		promptCtxBuilder.WriteString(" ")
		promptCtxBuilder.WriteString(strings.TrimSpace(m.surroundingTail))
	}
	promptCtx := strings.TrimSpace(promptCtxBuilder.String())

	var out strings.Builder
	cursor := 0
	for _, m := range matches {
		// 先把 cursor → m.start 之间的未匹配文本原样写出。
		if m.start > cursor {
			out.WriteString(text[cursor:m.start])
		}
		b64, mime, rok := resolveLocalImageToB64(m.path)
		if !rok {
			// 静默 miss:把原路径 token(含截断后的净路径 + surroundingTail)原样写回,不插占位。
			out.WriteString(text[m.start:m.end])
			out.WriteString(m.surroundingTail)
			cursor = m.end + len(m.surroundingTail)
			continue
		}
		ocrText, ocrErr, _ := s.OcrImage(userSession, b64, mime, promptCtx)
		if ocrErr != nil || strings.TrimSpace(ocrText) == "" {
			// OCR 失败/空文本:静默 miss,原样写回(不插 imageNotExtractablePlaceholder,与方案 §2 一致)。
			out.WriteString(text[m.start:m.end])
			out.WriteString(m.surroundingTail)
			cursor = m.end + len(m.surroundingTail)
			continue
		}
		// 成功:用 descHeader 替换路径 token,保留 surroundingTail(尾随 CJK 等)。
		out.WriteString(nvidiaImageOcrDescHeader(ocrModel, ocrText))
		out.WriteString(m.surroundingTail)
		replaced++
		cursor = m.end + len(m.surroundingTail)
	}
	// 尾部未匹配文本。
	if cursor < len(text) {
		out.WriteString(text[cursor:])
	}
	return out.String(), replaced
}

// EnrichLocalImagePathsInAnthropic 遍历 AnthropicRequest 所有消息的 content 块,对
// Type=="text" 的块调 resolveLocalImagePathsInText 原地改写其 Text。
//
// 返回成功注入 OCR 文本的路径总数(供调用方日志)。无路径命中或静默 miss 时返回 0,
// req 零变更。置于各号池入口现有 L2 image 降级(DowngradeAnthropicImagesToText)之前,
// 由调用方用 ocrSelf 守卫短路 OCR→OCR 自递归。
func (s *OCRService) EnrichLocalImagePathsInAnthropic(req *AnthropicRequest, userSession *RelaySession) int {
	if s == nil || req == nil {
		return 0
	}
	total := 0
	for mi := range req.Messages {
		for bi := range req.Messages[mi].Content {
			b := &req.Messages[mi].Content[bi]
			if b.Type != "text" || strings.TrimSpace(b.Text) == "" {
				continue
			}
			newText, replaced := s.resolveLocalImagePathsInText(b.Text, userSession)
			if replaced > 0 {
				b.Text = newText
				total += replaced
			}
		}
	}
	return total
}

// EnrichLocalImagePathsInGemini 遍历 GeminiRequest 所有 contents 的 parts,对 Text 非空的
// part 调 resolveLocalImagePathsInText 原地改写其 Text。
//
// 返回成功注入 OCR 文本的路径总数。与 Gemini 入站的 DowngradeGeminiImagesToText 配套,
// 置于其前。GeminiPart.Text 同时承载文本与(降级后的)图片描述,本层先扫 Text 裸路径注入,
// 不影响后续 InlineData 图的降级。
func (s *OCRService) EnrichLocalImagePathsInGemini(req *GeminiRequest, userSession *RelaySession) int {
	if s == nil || req == nil {
		return 0
	}
	total := 0
	for ci := range req.Contents {
		for pi := range req.Contents[ci].Parts {
			p := &req.Contents[ci].Parts[pi]
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			newText, replaced := s.resolveLocalImagePathsInText(p.Text, userSession)
			if replaced > 0 {
				p.Text = newText
				total += replaced
			}
		}
	}
	return total
}

// EnrichLocalImagePathsInOpenAIChat 在 raw body 层扫 OpenAI Chat/Responses 请求的
// messages[].content,对 string 形态与数组形态的 text 块都做裸路径注入。
//
// 仿 DowngradeOpenAIChatImagesToText 的 map[string]json.RawMessage 手法:不破坏
// 非文本/非 image 块,仅改写命中路径的 text 段。无路径命中或静默 miss 时原样返回
// 入参 bodyBytes(零变更)。返回 (newBody, replaced)。
//
// content 两种形态处理:
//   - string 形态(如 {"role":"user","content":"D:\\x\\a.png 看下"}):整串调
//     resolveLocalImagePathsInText,命中则 Marshal 回写;
//   - 数组形态(如 [{type:text,text:"..."},{type:image_url,...}]):逐个 text 块
//     (type ∈ openAITextBlockTypes)改写,非文本块原样保留;有命中则整体重 Marshal。
func (s *OCRService) EnrichLocalImagePathsInOpenAIChat(bodyBytes []byte, userSession *RelaySession) (newBody []byte, replaced int) {
	if s == nil || len(bodyBytes) == 0 {
		return bodyBytes, 0
	}
	// 入口闸在 resolveLocalImagePathsInText 内统一判定,这里提前返回避免无谓解析。
	if !s.isLocalDirectSession(userSession) {
		return bodyBytes, 0
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &obj); err != nil {
		return bodyBytes, 0
	}
	rawMsgs, ok := obj["messages"]
	if !ok {
		return bodyBytes, 0
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil || len(msgs) == 0 {
		return bodyBytes, 0
	}
	anyChanged := false
	for i := range msgs {
		rawContent, ok := msgs[i]["content"]
		if !ok || len(rawContent) == 0 {
			continue
		}
		trimmed := strings.TrimSpace(string(rawContent))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		if trimmed[0] != '[' {
			// string 形态:整串改写。
			var s2 string
			if err := json.Unmarshal(rawContent, &s2); err != nil {
				continue
			}
			newText, r := s.resolveLocalImagePathsInText(s2, userSession)
			if r > 0 {
				mb, merr := json.Marshal(newText)
				if merr == nil {
					msgs[i]["content"] = mb
					replaced += r
					anyChanged = true
				}
			}
			continue
		}
		// 数组形态:逐个 text 块改写,非文本块原样保留。
		var blocks []map[string]interface{}
		if err := json.Unmarshal(rawContent, &blocks); err != nil {
			continue
		}
		blockChanged := false
		for _, b := range blocks {
			t, _ := b["type"].(string)
			if !openAITextBlockTypes[t] {
				continue
			}
			txt, ok := b["text"].(string)
			if !ok || strings.TrimSpace(txt) == "" {
				continue
			}
			newText, r := s.resolveLocalImagePathsInText(txt, userSession)
			if r > 0 {
				b["text"] = newText
				replaced += r
				blockChanged = true
			}
		}
		if blockChanged {
			mb, merr := json.Marshal(blocks)
			if merr == nil {
				msgs[i]["content"] = mb
				anyChanged = true
			}
		}
	}
	if !anyChanged {
		return bodyBytes, 0
	}
	newMsgs, merr := json.Marshal(msgs)
	if merr != nil {
		return bodyBytes, replaced
	}
	obj["messages"] = newMsgs
	out, err := json.Marshal(obj)
	if err != nil {
		return bodyBytes, replaced
	}
	return out, replaced
}
