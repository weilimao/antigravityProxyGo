package tray

// SetupTray 初始化并启动系统托盘。
// onShow 当用户选择显示窗口时触发，onQuit 当用户选择退出应用时触发。
func SetupTray(onShow func(), onQuit func()) {
	setupTray(onShow, onQuit)
}

// QuitTray 关闭并释放系统托盘资源。
func QuitTray() {
	quitTray()
}
