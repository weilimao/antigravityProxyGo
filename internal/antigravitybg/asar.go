package antigravitybg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AsarHandler 负责 ASAR 包的解压、代码补丁注入与重打包。
type AsarHandler struct{}

// NewAsarHandler 创建 ASAR 处理器。
func NewAsarHandler() *AsarHandler {
	return &AsarHandler{}
}

const (
	runtimeHeaderTag = "// == Antigravity Custom Wallpaper Runtime =="
	runtimeFooterTag = "// == End Antigravity Custom Wallpaper Runtime =="
)

// ExtractAsar 解包 asar 文件到指定目录。
func (h *AsarHandler) ExtractAsar(asarPath, destDir string) error {
	_ = os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("创建解包临时目录失败: %w", err)
	}

	cmd := exec.Command("npx", "-y", "asar", "extract", asarPath, destDir)
	setHideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("asar 解包失败 (%v): %s", err, stderr.String())
	}

	return nil
}

// PackAsar 将指定目录打包为 asar 文件。
func (h *AsarHandler) PackAsar(srcDir, destAsarPath string) error {
	tmpAsar := destAsarPath + ".tmp"
	_ = os.Remove(tmpAsar)

	cmd := exec.Command("npx", "-y", "asar", "pack", srcDir, tmpAsar)
	setHideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpAsar)
		return fmt.Errorf("asar 打包失败 (%v): %s", err, stderr.String())
	}

	// 尝试原子重命名替换
	_ = os.Remove(destAsarPath)
	if err := os.Rename(tmpAsar, destAsarPath); err != nil {
		// 若 rename 失败（如跨分区或 transient lock），尝试 copy 覆盖替换
		if copyErr := copyFile(tmpAsar, destAsarPath); copyErr != nil {
			_ = os.Remove(tmpAsar)
			return fmt.Errorf("替换 asar 文件失败: %w (copy 备用亦失败: %v)", err, copyErr)
		}
		_ = os.Remove(tmpAsar)
	}

	return nil
}

// PatchPreload 在解包目录中的 dist/preload.js 文件中注入或更新壁纸运行时代码。
func (h *AsarHandler) PatchPreload(extractedDir string, cfg WallpaperConfig) error {
	preloadPath := filepath.Join(extractedDir, "dist", "preload.js")
	contentBytes, err := os.ReadFile(preloadPath)
	if err != nil {
		return fmt.Errorf("读取 preload.js 失败: %w", err)
	}

	content := string(contentBytes)
	runtimeCode, err := h.GenerateRuntimeJS(cfg)
	if err != nil {
		return fmt.Errorf("生成运行时代码失败: %w", err)
	}

	startIdx := strings.Index(content, runtimeHeaderTag)
	endIdx := strings.Index(content, runtimeFooterTag)

	var newContent string
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		newContent = content[:startIdx] + runtimeCode + content[endIdx+len(runtimeFooterTag):]
	} else {
		newContent = content + "\n\n" + runtimeCode
	}

	if err := os.WriteFile(preloadPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("写入注入补丁至 preload.js 失败: %w", err)
	}

	return nil
}

// PatchLanguageServer 在解包目录中的 dist/languageServer.js 文件中注入本地代理流量拦截代码。
func (h *AsarHandler) PatchLanguageServer(extractedDir string) error {
	lsPath := filepath.Join(extractedDir, "dist", "languageServer.js")
	contentBytes, err := os.ReadFile(lsPath)
	if err != nil {
		return nil // 若文件不存在则静默跳过
	}

	content := string(contentBytes)
	if strings.Contains(content, "env['HTTP_PROXY']  = 'http://127.0.0.1:18443'") ||
		strings.Contains(content, "env['HTTP_PROXY'] = 'http://127.0.0.1:18443'") {
		return nil // 已包含代理注入，无需重复修改
	}

	idx := strings.Index(content, "setupNodeWrapper)(env)")
	if idx == -1 {
		idx = strings.Index(content, "setupNodeWrapper)(process.env)")
	}
	if idx == -1 {
		return nil
	}

	stmtEnd := idx
	for stmtEnd < len(content) && content[stmtEnd] != ';' {
		stmtEnd++
	}
	if stmtEnd < len(content) {
		stmtEnd++ // 包含分号
	}

	stmtStart := idx
	for stmtStart > 0 && content[stmtStart] != '\n' && content[stmtStart] != ';' && content[stmtStart] != '{' {
		stmtStart--
	}
	if stmtStart > 0 {
		stmtStart++
	}

	wrapperCall := strings.TrimSpace(content[stmtStart:stmtEnd])
	injectStr := fmt.Sprintf(`%s
        // INJECTED BY ANTIGRAVITY PROXY DESKTOP
        env['HTTP_PROXY']  = 'http://127.0.0.1:18443';
        env['HTTPS_PROXY'] = 'http://127.0.0.1:18443';
        env['http_proxy']  = 'http://127.0.0.1:18443';
        env['https_proxy'] = 'http://127.0.0.1:18443';
        env['NO_PROXY']    = 'localhost,127.0.0.1';
        env['no_proxy']    = 'localhost,127.0.0.1';`, wrapperCall)

	newContent := content[:stmtStart] + injectStr + content[stmtEnd:]
	if err := os.WriteFile(lsPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("写入代理补丁至 languageServer.js 失败: %w", err)
	}

	return nil
}

// CheckIfPatched 检查解包目录或代码内容中是否已包含壁纸补丁。
func (h *AsarHandler) CheckIfPatched(preloadContent string) bool {
	return strings.Contains(preloadContent, runtimeHeaderTag) &&
		strings.Contains(preloadContent, runtimeFooterTag)
}

// RemovePatchFromContent 移除代码中的壁纸补丁。
func (h *AsarHandler) RemovePatchFromContent(content string) string {
	startIdx := strings.Index(content, runtimeHeaderTag)
	endIdx := strings.Index(content, runtimeFooterTag)
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		return strings.TrimSpace(content[:startIdx] + content[endIdx+len(runtimeFooterTag):])
	}
	return content
}

// PatchCustomScheme 在解包目录中的 dist/customScheme.js 中注册原生 ag-media 特权流式协议。
func (h *AsarHandler) PatchCustomScheme(extractedDir string) error {
	csPath := filepath.Join(extractedDir, "dist", "customScheme.js")
	contentBytes, err := os.ReadFile(csPath)
	if err != nil {
		return nil // 若文件不存在则静默跳过
	}

	content := string(contentBytes)
	if strings.Contains(content, "scheme: 'ag-media'") || strings.Contains(content, `scheme: "ag-media"`) {
		return nil // 已包含特权协议注册
	}

	// 1. 注入特权协议：直接在 registerSchemesAsPrivileged([ 数组首项安全插入
	targetPrivileged := "electron_1.protocol.registerSchemesAsPrivileged(["
	replacementPrivileged := `electron_1.protocol.registerSchemesAsPrivileged([
        {
            scheme: 'ag-media',
            privileges: {
                standard: true,
                secure: true,
                supportFetchAPI: true,
                corsEnabled: true,
                stream: true,
                bypassCSP: true,
            },
        },`
	if strings.Contains(content, targetPrivileged) {
		content = strings.Replace(content, targetPrivileged, replacementPrivileged, 1)
	}

	// 2. 注入协议处理器：直接在 registerCustomSchemeHandlers() 开头安全插入
	targetHandler := "function registerCustomSchemeHandlers() {"
	replacementHandler := `function registerCustomSchemeHandlers() {
    try {
        electron_1.protocol.handle('ag-media', (request) => {
            const urlModule = require('url');
            const fsModule = require('fs');
            let rawPath = decodeURIComponent(request.url.replace(/^ag-media:\/\/+/, ''));
            if (/^[a-zA-Z]\//.test(rawPath)) {
                rawPath = rawPath[0].toUpperCase() + ':/' + rawPath.slice(2);
            } else if (/^\/[a-zA-Z]\//.test(rawPath)) {
                rawPath = rawPath[1].toUpperCase() + ':/' + rawPath.slice(3);
            } else if (/^\/[a-zA-Z]:/.test(rawPath)) {
                rawPath = rawPath.slice(1);
            }
            if (fsModule.existsSync(rawPath)) {
                const fileUrl = urlModule.pathToFileURL(rawPath).href;
                return electron_1.net.fetch(fileUrl);
            }
            console.error('[ag-media] File not found:', rawPath);
            return new Response(null, { status: 404 });
        });
    } catch (e) {
        console.error('Failed to register ag-media handler:', e);
    }`
	if strings.Contains(content, targetHandler) {
		content = strings.Replace(content, targetHandler, replacementHandler, 1)
	}

	if err := os.WriteFile(csPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 customScheme.js 失败: %w", err)
	}
	return nil
}

// GenerateRuntimeJS 根据配置生成注入的运行时 JavaScript 代码。
func (h *AsarHandler) GenerateRuntimeJS(cfg WallpaperConfig) (string, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	codeLines := []string{
		runtimeHeaderTag,
		"(function () {",
		"  const STORAGE_KEY = 'antigravity_custom_wallpaper_v1';",
		fmt.Sprintf("  let injectedConfig = %s;", string(configJSON)),
		"",
		"  function getConfig() {",
		"    let cfg = injectedConfig || { enabled: false };",
		"    try {",
		"      localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg));",
		"    } catch (e) {}",
		"    return cfg;",
		"  }",
		"",
		"  function applyWallpaper(cfg) {",
		"    if (!cfg || !cfg.enabled || (!cfg.imageData && !cfg.filePath)) {",
		"      removeWallpaper();",
		"      return;",
		"    }",
		"",
		"    const isVideo = (cfg.mediaType === 'video') ||",
		"                    (typeof cfg.filePath === 'string' && (cfg.filePath.endsWith('.mp4') || cfg.filePath.endsWith('.webm') || cfg.filePath.endsWith('.mov') || cfg.filePath.endsWith('.mkv'))) ||",
		"                    (typeof cfg.imageData === 'string' && (cfg.imageData.startsWith('data:video/') || cfg.imageData.endsWith('.mp4') || cfg.imageData.endsWith('.webm') || cfg.imageData.endsWith('.mov') || cfg.imageData.includes('.mp4?') || cfg.imageData.includes('.webm?')));",
		"",
		"    let targetPath = cfg.filePath || '';",
		"    if (!targetPath && typeof cfg.imageData === 'string' && cfg.imageData.includes('/local-media?path=')) {",
		"      try {",
		"        const u = new URL(cfg.imageData, 'http://127.0.0.1');",
		"        targetPath = decodeURIComponent(u.searchParams.get('path') || '');",
		"      } catch (e) {}",
		"    }",
		"",
		"    let mediaUrl = '';",
		"    if (targetPath) {",
		"      let clean = targetPath.replace(/\\\\/g, '/');",
		"      if (clean.startsWith('/')) clean = clean.slice(1);",
		"      mediaUrl = 'ag-media:///' + encodeURI(clean);",
		"    } else if (cfg.imageData) {",
		"      mediaUrl = cfg.imageData;",
		"    }",
		"",
		"    const sidebarTextColor = cfg.sidebarTextColor || '#f1f5f9';",
		"    const contentTextColor = cfg.contentTextColor || '#ffffff';",
		"    const userBubbleBg = cfg.userMessageBgColor || 'rgba(255, 255, 255, 0.08)';",
		"    const agentBubbleBg = cfg.agentMessageBgColor || 'rgba(15, 18, 28, 0.75)';",
		"    const sidebarBg = cfg.sidebarBgColor || 'rgba(12, 15, 24, 0.85)';",
		"    const composerBg = cfg.composerBgColor || 'rgba(18, 22, 34, 0.88)';",
		"    const modalBg = cfg.modalBgColor || 'rgba(18, 22, 34, 0.94)';",
		"    const codeBlockBg = cfg.codeBlockBgColor || 'rgba(10, 12, 20, 0.92)';",
		"",
		"    let styleEl = document.getElementById('ag-custom-wallpaper-style');",
		"    if (!styleEl) {",
		"      styleEl = document.createElement('style');",
		"      styleEl.id = 'ag-custom-wallpaper-style';",
		"      (document.head || document.documentElement).appendChild(styleEl);",
		"    }",
		"",
		"    const opacity = (typeof cfg.opacity === 'number') ? cfg.opacity : 0.35;",
		"    const blur = (typeof cfg.blur === 'number') ? cfg.blur : 8;",
		"    const darkOverlay = (typeof cfg.darkOverlay === 'number') ? cfg.darkOverlay : 0.3;",
		"    const glassAlpha = (typeof cfg.glassAlpha === 'number') ? cfg.glassAlpha : 0.85;",
		"    const fit = cfg.backgroundFit || 'cover';",
		"",
		"    styleEl.textContent = `",
		"      :root {",
		"        --background: transparent !important;",
		"        --color-background: transparent !important;",
		"        --card: ${modalBg} !important;",
		"        --color-card: ${modalBg} !important;",
		"        --card-border: transparent !important;",
		"        --muted: rgba(18, 22, 34, 0.6) !important;",
		"        --color-muted: rgba(18, 22, 34, 0.6) !important;",
		"        --surface: rgba(18, 22, 34, 0.45) !important;",
		"        --surface-container: rgba(18, 22, 34, 0.45) !important;",
		"        --surface-container-low: rgba(18, 22, 34, 0.3) !important;",
		"        --surface-container-high: rgba(18, 22, 34, 0.5) !important;",
		"        --vscode-sideBar-background: ${sidebarBg} !important;",
		"        --vscode-editor-background: ${codeBlockBg} !important;",
		"        --vscode-editorGutter-background: ${codeBlockBg} !important;",
		"        --vscode-activityBar-background: ${sidebarBg} !important;",
		"        --vscode-editorGroup-border: transparent !important;",
		"        --vscode-sideBar-border: transparent !important;",
		"        --vscode-panel-border: transparent !important;",
		"        --vscode-activityBar-border: transparent !important;",
		"        --vscode-tab-border: transparent !important;",
		"        --vscode-tab-activeBorder: transparent !important;",
		"        --vscode-tab-activeBorderTop: transparent !important;",
		"        --vscode-titleBar-border: transparent !important;",
		"        --vscode-statusBar-border: transparent !important;",
		"        --vscode-splitView-border: transparent !important;",
		"        --vscode-contrastBorder: transparent !important;",
		"        --vscode-tree-indentGuidesStroke: transparent !important;",
		"        --vscode-editorWidget-border: transparent !important;",
		"        --ag-bg-opacity: ${opacity};",
		"        --ag-bg-blur: ${blur}px;",
		"        --ag-bg-dark-overlay: ${darkOverlay};",
		"        --ag-ui-glass-alpha: ${glassAlpha};",
		"        --ag-ui-blur: ${blur}px;",
		"        --ag-sidebar-text-color: ${sidebarTextColor};",
		"        --ag-content-text-color: ${contentTextColor};",
		"        --foreground: ${contentTextColor} !important;",
		"        --color-foreground: ${contentTextColor} !important;",
		"        --muted-foreground: ${contentTextColor} !important;",
		"        --color-muted-foreground: ${contentTextColor} !important;",
		"        --ag-user-bubble-bg: ${userBubbleBg};",
		"        --ag-agent-bubble-bg: ${agentBubbleBg};",
		"        --ag-sidebar-bg: ${sidebarBg};",
		"        --ag-composer-bg: ${composerBg};",
		"        --ag-modal-bg: ${modalBg};",
		"        --ag-code-block-bg: ${codeBlockBg};",
		"      }",
		"",
		"      /* 1. 根视口全屏防溢出 & 隐藏外层多余滚动条 */",
		"      html, body, #root, #app, #__next, .monaco-workbench {",
		"        overflow: hidden !important;",
		"        width: 100% !important;",
		"        height: 100% !important;",
		"        margin: 0 !important;",
		"        padding: 0 !important;",
		"        border: none !important;",
		"      }",
		"",
		"      html::-webkit-scrollbar, body::-webkit-scrollbar {",
		"        display: none !important;",
		"        width: 0 !important;",
		"        height: 0 !important;",
		"      }",
		"",
		"      #ag-custom-wallpaper-container {",
		"        position: fixed !important;",
		"        inset: 0 !important;",
		"        top: 0 !important;",
		"        left: 0 !important;",
		"        width: 100% !important;",
		"        height: 100% !important;",
		"        pointer-events: none !important;",
		"        z-index: -1 !important;",
		"        overflow: hidden !important;",
		"        contain: strict !important;",
		"        margin: 0 !important;",
		"        padding: 0 !important;",
		"      }",
		"      #ag-custom-wallpaper-img {",
		"        position: absolute !important;",
		"        inset: -20px !important;",
		"        background-size: ${fit} !important;",
		"        background-position: center center !important;",
		"        background-repeat: no-repeat !important;",
		"        opacity: var(--ag-bg-opacity) !important;",
		"        filter: blur(var(--ag-bg-blur)) !important;",
		"        transform: scale(1.02) !important;",
		"      }",
		"      #ag-custom-wallpaper-video {",
		"        position: absolute !important;",
		"        inset: -20px !important;",
		"        width: calc(100% + 40px) !important;",
		"        height: calc(100% + 40px) !important;",
		"        object-fit: ${fit} !important;",
		"        opacity: var(--ag-bg-opacity) !important;",
		"        filter: blur(var(--ag-bg-blur)) !important;",
		"        transform: scale(1.02) !important;",
		"      }",
		"      #ag-custom-wallpaper-overlay {",
		"        position: absolute !important;",
		"        inset: 0 !important;",
		"        background: radial-gradient(circle at center, rgba(12, 15, 25, calc(var(--ag-bg-dark-overlay) * 0.75)), rgba(6, 8, 14, var(--ag-bg-dark-overlay))) !important;",
		"        pointer-events: none !important;",
		"      }",
		"",
		"      /* 2. 根容器与主对话流透光 (全景壁纸穿透，清除一切生硬边框) */",
		"      html, body, #root, #app, #__next, .monaco-workbench, .monaco-workbench .part:not(.editor),",
		"      .bg-background, [class*=\"bg-background\"], [class*=\"bg-surface\"],",
		"      main, [role=\"main\"], [class*=\"chat-content\"], [class*=\"messages-container\"],",
		"      main div[class*=\"flex-1\"][class*=\"flex-col\"], main div[class*=\"flex-1\"][class*=\"overflow\"] {",
		"        background: transparent !important;",
		"        background-color: transparent !important;",
		"        border-color: transparent !important;",
		"      }",
		"",
		"      .monaco-workbench .part > .content,",
		"      .monaco-workbench .monaco-sash {",
		"        border: none !important;",
		"        box-shadow: none !important;",
		"      }",
		"",
		"      /* 3. 左侧边栏全屏透光、文字颜色与 Hover 高光 (去除生硬右侧竖线) */",
		"      aside, nav, [class*=\"sidebar\"], [class*=\"SideBar\"], [class*=\"conversation-list\"] {",
		"        background-color: var(--ag-sidebar-bg) !important;",
		"        backdrop-filter: blur(var(--ag-ui-blur)) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(var(--ag-ui-blur)) saturate(180%) !important;",
		"        border-right: none !important;",
		"        border: none !important;",
		"        color: var(--ag-sidebar-text-color) !important;",
		"        z-index: 10 !important;",
		"      }",
		"",
		"      aside *, nav *, [class*=\"sidebar\"] *, [class*=\"SideBar\"] * {",
		"        color: var(--ag-sidebar-text-color) !important;",
		"      }",
		"",
		"      aside a:hover, nav a:hover, aside button:hover, nav button:hover,",
		"      [class*=\"sidebar\"] a:hover, [class*=\"sidebar\"] button:hover,",
		"      [class*=\"conversation-item\"]:hover, [class*=\"project-item\"]:hover, [class*=\"nav-item\"]:hover {",
		"        background-color: rgba(255, 255, 255, 0.08) !important;",
		"        border-radius: 8px !important;",
		"        border: none !important;",
		"        transition: background-color 0.15s ease !important;",
		"      }",
		"",
		"      /* 4. 全局浮层、下拉菜单与右键菜单层级绝对置顶 (Top Layering z-index: 999999) */",
		"      .context-view,",
		"      .monaco-menu-container,",
		"      .menubar-menu-items-holder,",
		"      .monaco-menu,",
		"      .monaco-dropdown,",
		"      .quick-input-widget,",
		"      .suggest-widget,",
		"      .monaco-hover,",
		"      [data-radix-popper-content-wrapper],",
		"      [role=\"dialog\"], [class*=\"modal\"], [class*=\"dialog\"], [class*=\"settings-panel\"],",
		"      [class*=\"popup\"], [class*=\"dropdown\"], [class*=\"popover\"], [class*=\"menu\"],",
		"      div[class*=\"fixed\"][class*=\"inset-0\"]:has([role=\"dialog\"]),",
		"      div[class*=\"fixed\"][class*=\"z-50\"],",
		"      div[class*=\"fixed\"][class*=\"z-50\"] > div,",
		"      div[class*=\"fixed\"][class*=\"z-[100]\"],",
		"      div[class*=\"fixed\"][class*=\"z-[100]\"] > div {",
		"        z-index: 999999 !important;",
		"      }",
		"",
		"      [role=\"dialog\"], [class*=\"modal\"], [class*=\"dialog\"], [class*=\"settings-panel\"],",
		"      [class*=\"popup\"], [class*=\"dropdown\"], [class*=\"popover\"],",
		"      div[class*=\"fixed\"][class*=\"inset-0\"]:has([role=\"dialog\"]),",
		"      div[class*=\"fixed\"][class*=\"z-50\"] > div,",
		"      div[class*=\"fixed\"][class*=\"z-[100]\"] > div {",
		"        background-color: var(--ag-modal-bg) !important;",
		"        border: 1px solid rgba(255, 255, 255, 0.08) !important;",
		"        box-shadow: 0 20px 60px rgba(0, 0, 0, 0.7) !important;",
		"        backdrop-filter: blur(24px) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(24px) saturate(180%) !important;",
		"        border-radius: 16px !important;",
		"      }",
		"",
		"      /* 5. 用户输入与发送的消息气泡 (无界磨砂胶囊 + Hover 浮动响应) */",
		"      [data-testid=\"user-input-step\"] .bg-card,",
		"      [data-testid=\"user-input-step\"] [class*=\"bg-card\"],",
		"      [data-testid=\"user-input-step\"] > div {",
		"        background-color: var(--ag-user-bubble-bg) !important;",
		"        border: none !important;",
		"        border-radius: 14px !important;",
		"        backdrop-filter: blur(16px) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(16px) saturate(180%) !important;",
		"        box-shadow: 0 4px 20px rgba(0, 0, 0, 0.25) !important;",
		"        transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1) !important;",
		"      }",
		"",
		"      [data-testid=\"user-input-step\"]:hover .bg-card,",
		"      [data-testid=\"user-input-step\"]:hover [class*=\"bg-card\"],",
		"      [data-testid=\"user-input-step\"]:hover > div {",
		"        filter: brightness(1.2) !important;",
		"        box-shadow: 0 8px 28px rgba(0, 0, 0, 0.4) !important;",
		"      }",
		"",
		"      /* 6. Agent 助手回复卡片与 Hover 悬浮响应 (去生硬白边) */",
		"      [data-testid=\"agent-turn\"] > div,",
		"      [data-testid=\"assistant-message\"],",
		"      [class*=\"agent-message\"], [class*=\"bot-message\"], [class*=\"assistant-message\"] {",
		"        background-color: var(--ag-agent-bubble-bg) !important;",
		"        border: none !important;",
		"        border-radius: 14px !important;",
		"        backdrop-filter: blur(14px) !important;",
		"        -webkit-backdrop-filter: blur(14px) !important;",
		"        box-shadow: 0 4px 16px rgba(0, 0, 0, 0.25) !important;",
		"      [data-testid=\"agent-turn\"]:hover > div,",
		"      [data-testid=\"assistant-message\"]:hover,",
		"      [class*=\"agent-message\"]:hover, [class*=\"bubble\"]:hover {",
		"        filter: brightness(1.15) !important;",
		"        box-shadow: 0 6px 24px rgba(0, 0, 0, 0.35) !important;",
		"      }",
		"",
		"      [class*=\"message-card\"], [class*=\"bubble\"], [class*=\"chat-message\"], [class*=\"prose\"] {",
		"        color: var(--ag-content-text-color) !important;",
		"      }",
		"",
		"      [class*=\"message\"] p, [class*=\"prose\"] p, [class*=\"message\"] li, [class*=\"prose\"] li,",
		"      [class*=\"message\"] span, [class*=\"prose\"] span {",
		"        color: var(--ag-content-text-color) !important;",
		"        line-height: 1.6 !important;",
		"      }",
		"",
		"      [class*=\"message\"] h1, [class*=\"message\"] h2, [class*=\"message\"] h3, [class*=\"message\"] h4,",
		"      [class*=\"prose\"] h1, [class*=\"prose\"] h2, [class*=\"prose\"] h3 {",
		"        color: #ffffff !important;",
		"        font-weight: 700 !important;",
		"      }",
		"",
		"      /* 6.1. 工作区选择器目录与输入框工具栏文字/图标 (强制纯白高亮高对比) */",
		"      div:has(> form:has(textarea)) button,",
		"      div:has(> form:has(textarea)) span,",
		"      div:has(> form:has(textarea)) svg,",
		"      form:has(textarea) button,",
		"      form:has(textarea) span,",
		"      form:has(textarea) svg,",
		"      [class*=\"workspace\"],",
		"      [class*=\"header\"] button,",
		"      [class*=\"header\"] span {",
		"        color: var(--ag-content-text-color) !important;",
		"        fill: currentColor !important;",
		"        opacity: 0.95 !important;",
		"      }",
		"",
		"      div:has(> form:has(textarea)) button:hover,",
		"      form:has(textarea) button:hover,",
		"      [class*=\"workspace\"]:hover {",
		"        opacity: 1 !important;",
		"        filter: brightness(1.25) !important;",
		"      }",
		"",
		"      /* 7. 输入框外部包装层透明化 (防多层嵌套与防长横条底色) */",
		"      div:has(> form:has(textarea)),",
		"      div:has(> [data-testid=\"user-input-step\"]) {",
		"        background: transparent !important;",
		"        background-color: transparent !important;",
		"        backdrop-filter: none !important;",
		"        -webkit-backdrop-filter: none !important;",
		"        box-shadow: none !important;",
		"        border: none !important;",
		"      }",
		"",
		"      /* 7.1. 底部核心输入框 (唯一磨砂卡片载体，合三为一) */",
		"      form:has(textarea),",
		"      [class*=\"composer\"] {",
		"        background-color: var(--ag-composer-bg) !important;",
		"        border: 1px solid rgba(255, 255, 255, 0.08) !important;",
		"        box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5) !important;",
		"        backdrop-filter: blur(20px) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(20px) saturate(180%) !important;",
		"        border-radius: 16px !important;",
		"      }",
		"",
		"      textarea, input[type=\"text\"], input[type=\"search\"] {",
		"        background-color: transparent !important;",
		"        color: #ffffff !important;",
		"      }",
		"",
		"      textarea::placeholder, input::placeholder {",
		"        color: rgba(148, 163, 184, 0.75) !important;",
		"      }",
		"",
		"      /* 8. 代码块与 Monaco 编辑器 (全层级深度覆盖，动态绑定 codeBlockBg) */",
		"      pre, [class*=\"code-block\"], [class*=\"highlight-container\"], div:has(> pre),",
		"      div:has(> [class*=\"monaco-editor\"]),",
		"      div:has(> div > [class*=\"monaco-editor\"]),",
		"      div:has(> .monaco-editor),",
		"      div:has(> div > .monaco-editor),",
		"      div:has(> [class*=\"tabs-container\"]),",
		"      div:has(> [class*=\"breadcrumbs\"]),",
		"      div[class*=\"editor-group-container\"],",
		"      div[class*=\"editor-container\"],",
		"      div[class*=\"editor-instance\"],",
		"      [id=\"workbench.parts.editor\"],",
		"      .monaco-workbench .part.editor,",
		"      .monaco-workbench .part.editor > .content,",
		"      .monaco-workbench .part.editor .editor-group-container,",
		"      .monaco-workbench .part.editor .editor-container,",
		"      .monaco-workbench .part.editor .editor-instance,",
		"      .monaco-workbench .part.editor .tabs-and-actions-container,",
		"      .monaco-editor,",
		"      .monaco-editor-background,",
		"      .monaco-editor .overflow-guard,",
		"      .monaco-editor .monaco-scrollable-element,",
		"      .monaco-editor .lines-content,",
		"      .monaco-editor .inputarea.ime-input,",
		"      .monaco-editor .margin {",
		"        background-color: var(--ag-code-block-bg) !important;",
		"        backdrop-filter: blur(var(--ag-ui-blur)) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(var(--ag-ui-blur)) saturate(180%) !important;",
		"      }",
		"",
		"      pre, [class*=\"code-block\"], [class*=\"highlight-container\"], div:has(> pre) {",
		"        border: 1px solid rgba(255, 255, 255, 0.06) !important;",
		"        border-radius: 10px !important;",
		"      }",
		"",
		"      /* 9. 下拉弹窗与 VSCode 原生顶部/右键菜单 (Radix, Dropdown & Context-View) */",
		"      .context-view,",
		"      .monaco-menu-container,",
		"      .menubar-menu-items-holder,",
		"      .monaco-menu,",
		"      [data-radix-popper-content-wrapper] > div,",
		"      [role=\"menu\"], [role=\"listbox\"], [class*=\"dropdown-content\"], [class*=\"popover-content\"] {",
		"        background-color: var(--ag-modal-bg) !important;",
		"        border: 1px solid rgba(255, 255, 255, 0.08) !important;",
		"        border-radius: 12px !important;",
		"        backdrop-filter: blur(24px) saturate(180%) !important;",
		"        -webkit-backdrop-filter: blur(24px) saturate(180%) !important;",
		"        box-shadow: 0 20px 60px rgba(0, 0, 0, 0.8) !important;",
		"        color: #ffffff !important;",
		"      }",
		"",
		"      [role=\"menuitem\"], [role=\"option\"], [class*=\"dropdown-item\"] {",
		"        color: #f1f5f9 !important;",
		"        border-radius: 8px !important;",
		"        border: none !important;",
		"        transition: background-color 0.15s ease !important;",
		"      }",
		"",
		"      [role=\"menuitem\"]:hover, [role=\"option\"]:hover, [class*=\"dropdown-item\"]:hover,",
		"      [role=\"menuitem\"][data-highlighted], [role=\"option\"][data-highlighted] {",
		"        background-color: rgba(255, 255, 255, 0.12) !important;",
		"        color: #ffffff !important;",
		"      }",
		"    `;",
		"",
		"    let container = document.getElementById('ag-custom-wallpaper-container');",
		"    if (!container) {",
		"      container = document.createElement('div');",
		"      container.id = 'ag-custom-wallpaper-container';",
		"      if (document.body) {",
		"        document.body.prepend(container);",
		"      } else {",
		"        window.addEventListener('DOMContentLoaded', () => {",
		"          if (document.body && !document.getElementById('ag-custom-wallpaper-container')) {",
		"            document.body.prepend(container);",
		"          }",
		"        });",
		"      }",
		"    }",
		"",
		"    if (isVideo) {",
		"      let imgEl = document.getElementById('ag-custom-wallpaper-img');",
		"      if (imgEl) imgEl.remove();",
		"",
		"      let videoEl = document.getElementById('ag-custom-wallpaper-video');",
		"      if (!videoEl) {",
		"        videoEl = document.createElement('video');",
		"        videoEl.id = 'ag-custom-wallpaper-video';",
		"        videoEl.autoplay = true;",
		"        videoEl.loop = (cfg.loop !== false);",
		"        videoEl.muted = (cfg.muted !== false);",
		"        videoEl.playsInline = true;",
		"        videoEl.setAttribute('autoplay', '');",
		"        videoEl.setAttribute('loop', '');",
		"        videoEl.setAttribute('muted', '');",
		"        videoEl.setAttribute('playsinline', '');",
		"        videoEl.setAttribute('webkit-playsinline', '');",
		"        container.prepend(videoEl);",
		"      }",
		"      if (videoEl.src !== mediaUrl) {",
		"        videoEl.src = mediaUrl;",
		"      }",
		"      videoEl.playbackRate = (typeof cfg.playbackRate === 'number' && cfg.playbackRate > 0) ? cfg.playbackRate : 1.0;",
		"",
		"      const ensurePlay = () => {",
		"        if (!videoEl) return;",
		"        videoEl.muted = (cfg.muted !== false);",
		"        if (videoEl.ended || (videoEl.duration && videoEl.currentTime >= videoEl.duration)) {",
		"          videoEl.currentTime = 0;",
		"        }",
		"        if (videoEl.paused) {",
		"          const p = videoEl.play();",
		"          if (p !== undefined) {",
		"            p.catch(() => {",
		"              videoEl.muted = true;",
		"              videoEl.play().catch(() => {});",
		"            });",
		"          }",
		"        }",
		"      };",
		"",
		"      ensurePlay();",
		"      videoEl.oncanplay = ensurePlay;",
		"      videoEl.onloadeddata = ensurePlay;",
		"      videoEl.onpause = () => setTimeout(ensurePlay, 50);",
		"      videoEl.onstalled = () => { videoEl.currentTime = 0; ensurePlay(); };",
		"      videoEl.onwaiting = ensurePlay;",
		"      videoEl.onended = () => { videoEl.currentTime = 0; ensurePlay(); };",
		"",
		"      if (!window.__AG_BG_WATCHDOG__) {",
		"        window.__AG_BG_WATCHDOG__ = true;",
		"        const wakeVideo = () => {",
		"          const v = document.getElementById('ag-custom-wallpaper-video');",
		"          if (v) {",
		"            if (v.ended || (v.duration && v.currentTime >= v.duration)) {",
		"              v.currentTime = 0;",
		"            }",
		"            if (v.paused) {",
		"              const p = v.play();",
		"              if (p !== undefined) {",
		"                p.catch(() => {",
		"                  v.muted = true;",
		"                  v.play().catch(() => {});",
		"                });",
		"              }",
		"            }",
		"          }",
		"        };",
		"        document.addEventListener('visibilitychange', () => {",
		"          if (!document.hidden) wakeVideo();",
		"        });",
		"        window.addEventListener('focus', wakeVideo);",
		"        setInterval(wakeVideo, 1000);",
		"      }",
		"    } else {",
		"      let videoEl = document.getElementById('ag-custom-wallpaper-video');",
		"      if (videoEl) videoEl.remove();",
		"",
		"      let imgEl = document.getElementById('ag-custom-wallpaper-img');",
		"      if (!imgEl) {",
		"        imgEl = document.createElement('div');",
		"        imgEl.id = 'ag-custom-wallpaper-img';",
		"        container.prepend(imgEl);",
		"      }",
		"      imgEl.style.backgroundImage = `url('${mediaUrl}')`;",
		"    }",
		"",
		"    let overlay = document.getElementById('ag-custom-wallpaper-overlay');",
		"    if (!overlay) {",
		"      overlay = document.createElement('div');",
		"      overlay.id = 'ag-custom-wallpaper-overlay';",
		"      container.appendChild(overlay);",
		"    }",
		"  }",
		"",
		"  function removeWallpaper() {",
		"    const styleEl = document.getElementById('ag-custom-wallpaper-style');",
		"    if (styleEl) styleEl.remove();",
		"    const container = document.getElementById('ag-custom-wallpaper-container');",
		"    if (container) container.remove();",
		"  }",
		"",
		"  function ensureWallpaper() {",
		"    try {",
		"      applyWallpaper(getConfig());",
		"    } catch (e) {",
		"      console.error('[AntigravityWallpaper] apply error:', e);",
		"    }",
		"  }",
		"",
		"  if (document.readyState === 'loading') {",
		"    window.addEventListener('DOMContentLoaded', ensureWallpaper);",
		"  } else {",
		"    ensureWallpaper();",
		"  }",
		"  setTimeout(ensureWallpaper, 300);",
		"  setTimeout(ensureWallpaper, 1000);",
		"  setTimeout(ensureWallpaper, 2500);",
		"",
		"  window.__ANTIGRAVITY_WALLPAPER__ = {",
		"    apply: (c) => {",
		"      localStorage.setItem(STORAGE_KEY, JSON.stringify(c));",
		"      applyWallpaper(c);",
		"    },",
		"    get: () => getConfig(),",
		"    reset: () => {",
		"      localStorage.removeItem(STORAGE_KEY);",
		"      removeWallpaper();",
		"    }",
		"  };",
		"})();",
		runtimeFooterTag,
	}

	return strings.Join(codeLines, "\n"), nil
}
