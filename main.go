package main

import (
	"context"
	"embed"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"antigravity-proxy/internal/settings"
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

// isSilentStartRequested 检查当前是否为系统开机自启且用户配置了静默启动。
// 仅当两者均满足时才开启 StartHidden，日常用户手动双击启动时保持 StartHidden=false，
// 使得操作系统在进程创建之初就建立可见前台窗口，牢固锁定前台输入焦点，彻底杜绝焦点回退到外部浏览器。
func isSilentStartRequested() bool {
	isAutostart := false
	for _, arg := range os.Args {
		if arg == "--autostart" || arg == "-autostart" {
			isAutostart = true
			break
		}
	}
	if !isAutostart {
		return false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	var defaultUserData string
	if runtime.GOOS == "windows" {
		defaultUserData = filepath.Join(homeDir, "AppData", "Roaming", "antigravity-proxy-desktop")
	} else {
		defaultUserData = filepath.Join(homeDir, "Library", "Application Support", "antigravity-proxy-desktop")
	}
	mgr := settings.NewManager()
	mgr.Init(defaultUserData)
	return mgr.GetSilentStart()
}

func main() {
	// 内存与性能平衡配置：设置 Go 堆内存软上限为 256MB，GOGC 调整为 80，兼顾挂机轻量化与大流量突发稳定性
	debug.SetMemoryLimit(256 * 1024 * 1024)
	debug.SetGCPercent(80)

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

	// Set WebView2 environment variable: 精简无关功能模块，保留 GPU Shader 缓存消除启动卡顿，V8 堆放宽至 256MB 消除大数据渲染 OOM
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--mute-audio --disable-audio --disable-features=AudioServiceSandbox,VideoCaptureService,Translate,MediaRouter,SpareRendererForSitePerProcess,CalculateNativeWinOcclusion,InterestFeedContentSuggestions,OptimizationHints --renderer-process-limit=1 --disable-site-isolation-trials --disable-background-networking --disable-component-update --disable-extensions --disable-sync --js-flags=\"--max-old-space-size=256\"")

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
		StartHidden:      isSilentStartRequested(),
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       func(ctx context.Context) { app.shutdown() },
		OnBeforeClose:    app.onBeforeClose,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: true, // 彻底禁用独立 GPU Process 与 D3D11 交换链，节省 350MB+ 物理内存并消除启动脉冲
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
