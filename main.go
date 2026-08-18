package main

import (
	"context"
	"embed"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"antigravity-proxy/internal/singleinstance"
)

//go:embed all:frontend/dist
var assets embed.FS

// localMediaHandler 为本地壁纸图片与视频提供按需 HTTP 206 流式读取，实现 0 内存拷贝与 0 HTTP 内存缓存
func localMediaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/local-media") {
			filePath := r.URL.Query().Get("path")
			if filePath != "" {
				if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() {
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
					w.Header().Set("Pragma", "no-cache")
					w.Header().Set("Expires", "0")
					http.ServeFile(w, r, filePath)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func main() {
	// 将工作目录切换为可执行文件实际目录，确保自启动时工作目录正确，防止托盘初始化失败
	if exePath, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exePath))
	}

	if shouldCheckSingleInstance() {
		// Acquire single instance lock
		lock, err := singleinstance.TryLock("antigravity-proxy-desktop")
		if err != nil {
			singleinstance.ShowAlreadyRunningMessage()
			os.Exit(0)
		}
		defer lock.Unlock()
	}

	// Set WebView2 environment variable: 禁用无关功能模块与限制 V8 堆上限，同时保留 GPU 硬件加速以避免 CPU 软件解码/毛玻璃卷积导致内存与 CPU 暴涨
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--mute-audio --disable-audio --disable-features=AudioServiceSandbox,VideoCaptureService,Translate,MediaRouter --disable-breakpad --js-flags=\"--max-old-space-size=128\"")

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "antigravity-proxy",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: localMediaHandler(),
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		StartHidden:      true,
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       func(ctx context.Context) { app.shutdown() },
		OnBeforeClose:    app.onBeforeClose,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: true, // 彻底禁用独立 GPU Process 与 D3D11 交换链，节省 350MB+ 物理内存并消除启动 900MB 脉冲
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}

	// 进程级硬退出兜底：
	// 即便存在后台 goroutine (account cooldown ticker、netutil 探测、
	// sigcache cleaner 等未纳入统一退出信号的协程) 仍在运行并持有句柄，
	// os.Exit 也会强制终止进程，彻底杜绝"退出窗口后进程在任务管理器
	// 中残留不消失"的问题。OnShutdown 已在 wails.Run 返回前同步执行，
	// 关键资源 (proxy、relay、session SaveToDisk 等) 已在此之前落盘完成。
	os.Exit(0)
}
