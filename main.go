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
			// TryLock 失败 = 已有实例。激活已有实例窗口(A 方案),再退出本进程,
			// 让用户的"双击"转化为主动唤出已运行实例,而不是阻塞 1s 后弹提示框。
			singleinstance.ShowAlreadyRunningMessage()
			os.Exit(0)
		}
		defer lock.Unlock()
	}

	// Set WebView2 environment variable: 禁用无关功能模块与限制 V8 堆上限，同时保留 GPU 硬件加速以避免 CPU 软件解码/毛玻璃卷积导致内存与 CPU 暴涨。
	// 后三项 (--disable-gpu-*-cache / --prune-gpu-command-buffer) 与 v1.4.0 一致, 阻止 GPU 进程常驻累积着色器/程序缓存导致 renderer 内存膨胀。
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--mute-audio --disable-audio --disable-features=AudioServiceSandbox,VideoCaptureService,Translate,MediaRouter --disable-breakpad --js-flags=\"--max-old-space-size=128\" --disable-gpu-program-caches --disable-gpu-shader-disk-cache --prune-gpu-command-buffer")

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
	//   OnShutdown 已被 lifecycle.Coordinator 包裹,自身具备 3s 整体预算与单任务超时;
	//   即便 Coordinator 内仍有极端情况(如 Win32 内核级挂起)未在 3s 内返回,
	//   wails.Run 返回前的最后一步仍会走到这里,os.Exit 强制终结进程,
	//   彻底杜绝"退出窗口后进程在任务管理器中残留不消失"。
	os.Exit(0)
}
