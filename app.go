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
	"antigravity-proxy/internal/antigravitybg"
	"antigravity-proxy/internal/autotrigger"
	"antigravity-proxy/internal/benchmark"
	"antigravity-proxy/internal/corelog"
	"antigravity-proxy/internal/dialogs"
	"antigravity-proxy/internal/eventsgate"
	"antigravity-proxy/internal/externalconfig"
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
	aiPricingGen      *pricing.AIPriceGenerator
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
	// benchmarkScheduler 定时向配置的模型列表发最小流式请求测首帧/总耗时,
	// 经中继回环复用全部路由链路, 结果落 benchmark_results 并推 benchmark-updated 事件。
	benchmarkScheduler *benchmark.Scheduler
	pendingLogs          []string
	pendingLogsMu        sync.Mutex

	// netWatch 周期性监听本机网络连通性,网络从断→通时触发
	// 代理引擎重置 + 远程中继自动重连,修复"断网后程序废"。
	netWatch *proxy.NetWatch
	// reloginMu 防止自动重连与网络恢复回调并发重复 Login 同一远端。
	reloginMu sync.Mutex

	// externalConfigMgr 管理外部 Agent(如 OpenCode / Claude Code 等)的配置文件读写。
	externalConfigMgr *externalconfig.Manager

	// antigravityBgMgr 管理 Antigravity 桌面端壁纸与外观调谐。
	antigravityBgMgr *antigravitybg.Manager

	// eventsGate 节流前端事件派发,防止 stats/logs/state 等高频发往主线程
	// 把主线程消息队列挤爆引发连锁卡死。startup 内构造。
	eventsGate *eventsgate.Gate

	// quitOnce 保证异步退出只发起一次,避免重复 Quit 触发二次 shutdown。
	quitOnce sync.Once
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

// OpenFolderInExplorer 对外打开指定文件夹（文件管理器）。
// 依赖注入 dialogs.RevealCallback 的落地实现：导出保存成功后打开文件夹。
func (a *App) OpenFolderInExplorer(p string) {
	if p == "" {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("cmd", "/c", "start", "", p).Start()
	} else if runtime.GOOS == "darwin" {
		_ = exec.Command("open", p).Start()
	}
}

// SetWindowVisible 线程安全地设置窗口可见状态
func (a *App) SetWindowVisible(v bool) {
	a.isWindowVisibleMu.Lock()
	a.isWindowVisible = v
	a.isWindowVisibleMu.Unlock()
	if v && a.eventsGate != nil {
		// 窗口恢复可见时,立刻补偿推送一次最新日志数据,防止后台静默状态期间漏刷
		a.eventsGate.Emit("stats-updated", a.getStatsPayload(false))
	}
}

// asyncQuit 异步发起 Wails 退出流程,保证只调一次。
// 退出链路本身已有 Coordinator 控制总耗时;此函数仅负责一次性触发。
func (a *App) asyncQuit() {
	a.quitOnce.Do(func() {
		go func() {
			defer func() { _ = recover() }()
			wailsRuntime.Quit(a.ctx)
		}()
	})
}

// emitEvent 经由 eventsGate 派发前端事件,统一节流与"不可见丢弃"。
// 所有原本直接调 wailsRuntime.EventsEmit(a.ctx, ...) 的代码一律改走这里。
func (a *App) emitEvent(name string, payload any) {
	if a.eventsGate == nil {
		// startup 早期尚未构造时退化为直发,保证不丢
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, name, payload)
		}
		return
	}
	a.eventsGate.Emit(name, payload)
}

// showMainWindow 统一"显示主窗口并唤到前台"的语义入口。
//
// 恢复原实现:走 wailsRuntime.WindowShow 单一路径,异步 goroutine
// 仅为了避免阻塞调用方(systray 线程)。SetWindowVisible(true) 同步置位,
// 触发一次 stats 补偿。
//
// 调用点: app_tray.go 托盘"显示控制面板"/双击图标 + app_lifecycle.go domReady 自动显示。
func (a *App) showMainWindow() {
	go func() {
		defer func() { _ = recover() }()
		wailsRuntime.WindowShow(a.ctx)
	}()
	a.SetWindowVisible(true)
}

// IsWindowVisibleAndActive 检查窗口是否在前台且可见（非最小化且未隐藏）。
// 恢复原实现:WindowIsMinimised 由 Wails 主线程查询(短暂排队可接受),
// 与进程内自维护的 isWindowVisible 复合判定。
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
