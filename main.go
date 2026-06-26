package main

import (
	"context"
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"antigravity-proxy/internal/singleinstance"
)

//go:embed all:frontend/dist
var assets embed.FS

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

	// Set WebView2 environment variable to disable unused features (audio, video capture and crashpad) and restrict V8 heap size to save memory
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--mute-audio --disable-audio --disable-features=AudioServiceSandbox,VideoCaptureService --disable-breakpad --js-flags=\"--max-old-space-size=128\" --disable-gpu-program-caches --disable-gpu-shader-disk-cache --prune-gpu-command-buffer")

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "antigravity-proxy",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
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
			WebviewGpuIsDisabled: true, // Disable GPU acceleration to save memory
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
