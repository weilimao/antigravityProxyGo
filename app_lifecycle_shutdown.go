package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"antigravity-proxy/internal/corelog"
	"antigravity-proxy/internal/diagserver"
	"antigravity-proxy/internal/lifecycle"
	"antigravity-proxy/internal/patch"
	"antigravity-proxy/internal/proxy"
	"antigravity-proxy/internal/sigcache"
	"antigravity-proxy/internal/tray"
)

// app_lifecycle_shutdown.go: 从 app_lifecycle.go 拆出的网络恢复与退出收尾子函数。
// startNetWatch / onNetRecover / reconnectRemoteSafely / shutdown 物理搬移,逻辑逐行等价,零回归。

// startNetWatch 启动网络连通性监听器。
// 仅在配置了远程中继(或代理引擎在跑)时才有意义;未配置也不报错。
// 网络从离线恢复到在线时触发 onNetRecover 回调。
func (a *App) startNetWatch() {
	a.netWatch = proxy.NewNetWatch(a.onNetRecover)
	a.netWatch.Start()
	if a.settingsMgr.GetEnableSystemLog() {
		a.AddLog("📡 [网络监听] 网络连通性监听器已启动,断网恢复后将自动重置连接与重连远程中继")
	}
}

// onNetRecover 网络从断→通的边沿恢复回调。
// 重置代理引擎本地连接池(消除休眠/断网残留死连接),
// 并在已配置远程中继凭据时尝试自动重连。
func (a *App) onNetRecover() {
	a.AddLog("🌐 [网络恢复] 检测到网络已恢复,正在重置本地连接池与远程中继链路...")

	// 1. 重置代理引擎连接池与活跃隧道,强制客户端重建连接
	if a.proxyEngine != nil {
		a.proxyEngine.ResetConnections()
	}
	if a.remoteRelay != nil {
		a.proxyEngine.ResetRemoteClient()
	}

	// 2. 已配置远程中继凭据但当前未连接(或被健康检查标为离线)→ 自动重连
	if a.remoteRelay != nil && a.settingsMgr.GetRemoteEnabled() {
		host := a.settingsMgr.GetRemoteHost()
		port := a.settingsMgr.GetRemotePort()
		path := a.settingsMgr.GetRemotePath()
		key := a.settingsMgr.GetRemoteKey()
		pwd := a.settingsMgr.GetRemotePassword()
		if host != "" && key != "" {
			go func() {
				if err := a.reconnectRemoteSafely(host, port, path, key, pwd); err != nil {
					a.AddLog(fmt.Sprintf("⚠️ [网络恢复] 自动重连远程中继失败: %v", err))
				} else {
					a.AddLog("✅ [网络恢复] 远程中继自动重连成功")
				}
			}()
		}
	}
}

// reconnectRemoteSafely 线程安全地执行一次远程中继重连。
// 通过 reloginMu 串行化,避免网络恢复回调与健康检查重连回调并发重复 Login。
func (a *App) reconnectRemoteSafely(host, port, path, key, pwd string) error {
	a.reloginMu.Lock()
	defer a.reloginMu.Unlock()

	// 若此刻已连接,无需重连(健康检查可能已抢先恢复)
	if a.remoteRelay != nil && a.remoteRelay.IsConnected() {
		return nil
	}
	return a.connectRemote(host, port, path, key, pwd)
}

// shutdown 关闭流程 — lifecycle.Coordinator 两阶段并发执行。
//
// 阶段1 (并发, 单任务 ≤1.5s): 停所有"接收新工作"端点
//   - tray / netWatch / autoTriggerScheduler / monitorCancel / relay / proxyEngine
//     任一单点卡 1.5s 会被该任务自身超时切断,不再拖累其他关闭项。
//
// 阶段2 (并发, 单任务 ≤1s): 落盘/写文件等"完成后即安全退出"的动作
//   - sessionRouter.SaveToDisk / account.Stop*Monitor / PatchAll(false) / corelog.Stop /
//     sigcache.StopGlobal / diagserver.Stop
//
// 整体预算 ≤ 3s,到点未完成任务由 lifecycle.Coordinator 返回"未完成清单"供 main 末尾
// 的 os.Exit(0) 兜底终结进程。源头上消除"右键退出后任务管理器残留"。
func (a *App) shutdown() {
	coord := &lifecycle.Coordinator{
		OverallBudget: 3 * time.Second,
		Logf: func(format string, args ...interface{}) {
			// 静默即可,失败信息已通过 lifecycle 内部 Logf 注入
			_ = fmt.Sprintf(format, args...)
		},
	}

	// 阶段1:并发停所有"接收新工作"端点
	stage1 := []lifecycle.Task{
		{
			Name:    "tray",
			Timeout: 800 * time.Millisecond,
			Run: func(ctx context.Context) error {
				tray.QuitTray()
				return nil
			},
		},
		{
			Name:    "netWatch",
			Timeout: 500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.netWatch != nil {
					a.netWatch.Stop()
				}
				return nil
			},
		},
		{
			Name:    "autoTrigger",
			Timeout: 1500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.autoTriggerScheduler != nil {
					a.autoTriggerScheduler.Stop()
				}
				return nil
			},
		},
		{
			Name:    "monitorCancel",
			Timeout: 200 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.monitorCancel != nil {
					a.monitorCancel()
				}
				return nil
			},
		},
		{
			Name:    "relay",
			Timeout: 1200 * time.Millisecond,
			Run: func(ctx context.Context) error {
				a.stopRelayServer()
				return nil
			},
		},
		{
			Name:    "proxyEngine",
			Timeout: 1500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.proxyEngine != nil {
					a.proxyEngine.Stop()
				}
				return nil
			},
		},
	}
	_ = coord.Run(context.Background(), stage1)

	// 阶段2:并发落盘/收尾(任一慢 ≠ 拖累其他)
	stage2 := []lifecycle.Task{
		{
			Name:    "session.SaveToDisk",
			Timeout: 800 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.sessionRouter != nil {
					a.sessionRouter.SaveToDisk()
				}
				return nil
			},
		},
		{
			Name:    "account.StopMonitors",
			Timeout: 500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				if a.accountMgr != nil {
					a.accountMgr.StopCooldownMonitor()
					a.accountMgr.StopTokenRefreshMonitor()
					a.accountMgr.StopGrokAuthMonitor()
				}
				return nil
			},
		},
		{
			Name:    "patch.Unpatch",
			Timeout: 1000 * time.Millisecond,
			Run: func(ctx context.Context) error {
				homeDir, _ := os.UserHomeDir()
				activeDir := a.settingsMgr.GetActiveDataDirectory()
				caCertPath := filepath.Join(activeDir, "certs", "certs", "ca.pem")
				_ = patch.PatchAll(false, a.settingsMgr.GetDefaultUserDataPath(), homeDir, caCertPath, func(s string) {})
				return nil
			},
		},
		{
			Name:    "corelog.Stop",
			Timeout: 500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				corelog.Stop()
				return nil
			},
		},
		{
			Name:    "sigcache.StopGlobal",
			Timeout: 300 * time.Millisecond,
			Run: func(ctx context.Context) error {
				sigcache.StopGlobal()
				return nil
			},
		},
		{
			Name:    "diagserver.Stop",
			Timeout: 500 * time.Millisecond,
			Run: func(ctx context.Context) error {
				diagserver.Stop()
				return nil
			},
		},
	}
	_ = coord.Run(context.Background(), stage2)
}
