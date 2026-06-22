package main

import (
	"context"
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
