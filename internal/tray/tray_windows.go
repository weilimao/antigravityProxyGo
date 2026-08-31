//go:build windows

package tray

import (
	"bytes"
	_ "embed"
	"image/png"
	"runtime"
	"syscall"

	"github.com/energye/systray"
)

//go:embed tray-icon.png
var trayIconPng []byte

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultUILanguage  = kernel32.NewProc("GetUserDefaultUILanguage")
)

var (
	onShowCallback func()
	onQuitCallback func()
)

func setupTray(onShow func(), onQuit func()) {
	onShowCallback = onShow
	onQuitCallback = onQuit

	// 必须锁定 OS 线程:Windows 托盘窗口的消息队列线程亲和,
	// GetMessage 必须在创建窗口的那个线程上泵送。库自身的 init() 虽
	// 调了 LockOSThread,但锁的是主 goroutine,不覆盖此处新启的 goroutine。
	// 未锁定的 goroutine 会被 runtime 迁移到其他 OS 线程,届时点击消息滞留
	// 旧线程队列、GetMessage 在新线程取空队列 → 幽灵图标(可见但点击无响应)。
	go func() {
		runtime.LockOSThread()
		systray.Run(onReady, onExit)
	}()
}

func quitTray() {
	systray.Quit()
}

func isChineseLocale() bool {
	// 调用 GetUserDefaultUILanguage 获取语言 ID
	r1, _, _ := procGetUserDefaultUILanguage.Call()
	if r1 == 0 {
		return false
	}
	// Primary language ID 0x04 is LANG_CHINESE
	return (r1 & 0xFF) == 0x04
}

func pngToIco(pngData []byte) []byte {
	size := uint32(len(pngData))
	
	// 动态解析 PNG 尺寸以防万一
	width, height := 16, 16
	img, err := png.Decode(bytes.NewReader(pngData))
	if err == nil {
		width = img.Bounds().Dx()
		height = img.Bounds().Dy()
	}

	ico := make([]byte, 22+size)

	// ICO Header
	ico[0] = 0x00
	ico[1] = 0x00
	ico[2] = 0x01
	ico[3] = 0x00
	ico[4] = 0x01
	ico[5] = 0x00

	// Directory Entry
	ico[6] = byte(width)
	ico[7] = byte(height)
	ico[8] = 0x00 // Color count
	ico[9] = 0x00 // Reserved

	// Planes (2 bytes, always 1)
	ico[10] = 0x01
	ico[11] = 0x00

	// BitCount (2 bytes, 32 bits)
	ico[12] = 0x20
	ico[13] = 0x00

	// BytesInRes (4 bytes, size of PNG)
	ico[14] = byte(size)
	ico[15] = byte(size >> 8)
	ico[16] = byte(size >> 16)
	ico[17] = byte(size >> 24)

	// ImageOffset (4 bytes, 22)
	ico[18] = 22
	ico[19] = 0x00
	ico[20] = 0x00
	ico[21] = 0x00

	// Copy PNG data
	copy(ico[22:], pngData)

	return ico
}

func onReady() {
	icoBytes := pngToIco(trayIconPng)
	systray.SetIcon(icoBytes)
	systray.SetTooltip("Antigravity Proxy")

	systray.SetOnDClick(func(menu systray.IMenu) {
		if onShowCallback != nil {
			onShowCallback()
		}
	})

	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})

	var showLabel, quitLabel string
	if isChineseLocale() {
		showLabel = "显示控制面板"
		quitLabel = "退出代理引擎"
	} else {
		showLabel = "Show Dashboard"
		quitLabel = "Quit Proxy Engine"
	}

	mShow := systray.AddMenuItem(showLabel, showLabel)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(quitLabel, quitLabel)

	mShow.Click(func() {
		if onShowCallback != nil {
			onShowCallback()
		}
	})

	mQuit.Click(func() {
		if onQuitCallback != nil {
			onQuitCallback()
		}
	})
}

func onExit() {
	// Cleanup on exit if needed
}
