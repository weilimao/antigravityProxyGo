package main

// app.go: 应用主入口 — App 结构体定义 + 构造函数 + 短工具方法(日志/路径/窗口可见性)。
// 原单文件 1975 行已按职责拆分,本文件只保留结构体与直接依附其上的小工具方法,
// 各业务块按职责拆到卫星文件(同 main 包内共享符号,逻辑逐行等价,零回归):
//   app_lifecycle.go   生命周期(startup/shutdown/domReady/网络恢复重连)
//   app_ipc.go          前端 IPC 路由(IPCSend/IPCInvoke)
//   app_monitor.go      后台监控(内存心跳/统计载荷)
//   app_relay.go        relay 文件中继服务装配(既有)
//   app_account_ipc.go / app_settings_ipc.go / app_session_ipc.go / app_totp_ipc.go / app_autotrigger_ipc.go  各域 IPC(既有)

import (
	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/corelog"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/pricing"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/quota"
	"antigravity-proxy/internal/relay"
	"antigravity-proxy/internal/session"
	"antigravity-proxy/internal/settings"
	"antigravity-proxy/internal/stats"
	"antigravity-proxy/internal/update"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx               context.Context
	settingsMgr       *settings.Manager
	accountMgr        *account.Manager
	sessionRouter     *session.Router
	pricingMgr        *pricing.Manager
	statsTracker      *stats.Tracker
	usageTracker      *stats.UsageTracker
	errLogger         *stats.RetryErrorLogger
	packetCap         *stats.PacketCapturer
	authMgr           *quota.AuthManager
	proxyEngine       *proxy.ProxyEngine
	updateMgr         *update.Manager
	dialogSvc         dialogs.Dialogs
	logBuffer         []string
	logBufferMu       sync.Mutex
	monitorCancel     context.CancelFunc
	quotaSvc          *quota.QuotaService
	isQuitting        bool
	isQuittingMu      sync.RWMutex
	isWindowVisible   bool
	isWindowVisibleMu sync.RWMutex
	// Relay server components
	relayUserMgr         *relay.UserManager
	relayPackageMgr      *relay.PackageManager
	relayAuthMgr         *relay.AuthManager
	relayStatsMgr        *relay.StatsTracker
	relayAPIMgr          *relay.APIHandler
	relayCompatAPIMgr    *relay.APICompatHandler
	relayServer          *relay.RelayServer
	remoteRelay          *proxy.RemoteRelay
	autoTriggerScheduler *autotrigger.Scheduler
	pendingLogs          []string
	pendingLogsMu        sync.Mutex

	// netWatch 周期性监听本机网络连通性,网络从断→通时触发
	// 代理引擎重置 + 远程中继自动重连,修复"断网后程序废"。
	netWatch *proxy.NetWatch
	// reloginMu 防止自动重连与网络恢复回调并发重复 Login 同一远端。
	reloginMu sync.Mutex
}

func NewApp() *App {
	return &App{
		logBuffer: make([]string, 0),
	}
}

func (a *App) AddLog(msg string) {
	if a.settingsMgr != nil && !a.settingsMgr.GetEnableSystemLog() {
		return
	}
	timestamp := time.Now().Format("15:04:05.000")
	formatted := fmt.Sprintf("[%s] %s", timestamp, msg)

	// 同时输出至标准输出，以便在终端中展示日志。
	// 使用 corelog 异步、永不阻塞的 writer：即便 Wails stdout 转发管道
	// 下游停止消费，也不会反向阻塞请求处理 goroutine，从根本杜绝
	// "数十秒后整体卡死、接口全阻塞"级联阻塞。
	corelog.Println(formatted)

	a.logBufferMu.Lock()
	a.logBuffer = append(a.logBuffer, formatted)
	if len(a.logBuffer) > 50 {
		a.logBuffer = a.logBuffer[1:]
	}
	a.logBufferMu.Unlock()

	a.pendingLogsMu.Lock()
	a.pendingLogs = append(a.pendingLogs, formatted)
	if len(a.pendingLogs) > 200 {
		a.pendingLogs = a.pendingLogs[1:]
	}
	a.pendingLogsMu.Unlock()
}

// OpenPath opens system browser or path
func (a *App) OpenPath(p string) {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			wailsRuntime.BrowserOpenURL(a.ctx, p)
		} else {
			_ = exec.Command("cmd", "/c", "start", "", p).Start()
		}
	} else if runtime.GOOS == "darwin" {
		_ = exec.Command("open", p).Start()
	}
}

// ShowItemInFolder displays file in native file manager
func (a *App) ShowItemInFolder(p string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("explorer", "/select,", p).Start()
	} else if runtime.GOOS == "darwin" {
		_ = exec.Command("open", "-R", p).Start()
	}
}

// SetWindowVisible 线程安全地设置窗口可见状态
func (a *App) SetWindowVisible(v bool) {
	a.isWindowVisibleMu.Lock()
	a.isWindowVisible = v
	a.isWindowVisibleMu.Unlock()
	if v {
		// 窗口恢复可见时，立刻补偿推送一次最新日志数据，防止后台静默状态期间漏刷
		wailsRuntime.EventsEmit(a.ctx, "stats-updated", a.getStatsPayload(false))
	}
}

// IsWindowVisibleAndActive 检查窗口是否在前台且可见（非最小化且未隐藏）
func (a *App) IsWindowVisibleAndActive() bool {
	if a.ctx == nil {
		return false
	}
	if wailsRuntime.WindowIsMinimised(a.ctx) {
		return false
	}
	a.isWindowVisibleMu.RLock()
	defer a.isWindowVisibleMu.RUnlock()
	return a.isWindowVisible
}
