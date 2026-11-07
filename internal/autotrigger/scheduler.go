package autotrigger

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/db"
	"antigravity-proxy/internal/quota"
)

type Scheduler struct {
	accountMgr  *account.Manager
	quotaSvc    *quota.QuotaService
	authMgr     *quota.AuthManager
	addLog      func(string)
	ticker      *time.Ticker
	quit        chan struct{}
	wg          sync.WaitGroup
	runningMu   sync.Mutex
	runningJobs map[int64]bool // 避免同个任务并发执行重叠
	lastQuotaResetTrigger map[string]int64 // accountID -> cooldownUntil
}

func NewScheduler(accountMgr *account.Manager, quotaSvc *quota.QuotaService, authMgr *quota.AuthManager, addLog func(string)) *Scheduler {
	return &Scheduler{
		accountMgr:  accountMgr,
		quotaSvc:    quotaSvc,
		authMgr:     authMgr,
		addLog:      addLog,
		quit:                  make(chan struct{}),
		runningJobs:           make(map[int64]bool),
		lastQuotaResetTrigger: make(map[string]int64),
	}
}

// Start launches the background scheduler checking timer tasks
func (s *Scheduler) Start() {
	s.ticker = time.NewTicker(10 * time.Second)
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		s.addLog("⏰ [任务调度器] 定时触发任务调度器已成功启动！")
		for {
			select {
			case <-s.ticker.C:
				s.checkTimerTasks()
				s.checkQuotaResetTasks()
			case <-s.quit:
				return
			}
		}
	}()
}

// Stop stops the scheduler and waits for active jobs to finish
func (s *Scheduler) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.quit)
	s.wg.Wait()
	s.addLog("⏰ [任务调度器] 定时触发任务调度器已安全关闭。")
}

// checkQuotaResetTasks checks if any account has reached its CooldownUntil and triggers quota_refreshed tasks.
func (s *Scheduler) checkQuotaResetTasks() {
	tasks, err := db.ListAutoTriggerTasks()
	if err != nil {
		return
	}

	nowMs := time.Now().UnixNano() / int64(time.Millisecond)

	for _, task := range tasks {
		if !task.Enabled || task.TriggerType != "quota_refreshed" {
			continue
		}

		for _, accID := range task.AccountIDs {
			acc := s.accountMgr.GetAccountByID(accID)
			if acc == nil || !acc.Enabled || acc.CooldownUntil == 0 {
				continue
			}

			if nowMs >= acc.CooldownUntil {
				s.runningMu.Lock()
				lastTriggerTime := s.lastQuotaResetTrigger[acc.ID]
				if lastTriggerTime == acc.CooldownUntil {
					s.runningMu.Unlock()
					continue // Already triggered for this specific reset time
				}
				s.lastQuotaResetTrigger[acc.ID] = acc.CooldownUntil
				s.runningMu.Unlock()

				s.addLog(fmt.Sprintf("⚡ [自动测试] 检测到账号 %s 到达配额重置时间，开始触发自动化任务 [%s]...", acc.Email, task.Name))
				go s.runTask(task, acc.ID)
			}
		}
	}
}

func (s *Scheduler) checkTimerTasks() {
	tasks, err := db.ListAutoTriggerTasks()
	if err != nil {
		return
	}

	now := time.Now()
	for _, task := range tasks {
		if !task.Enabled || task.TriggerType != "timer" {
			continue
		}

		if task.NextTriggerTime != nil && now.After(*task.NextTriggerTime) {
			s.runningMu.Lock()
			if s.runningJobs[task.ID] {
				s.runningMu.Unlock()
				continue // 任务还在执行中，跳过
			}
			s.runningJobs[task.ID] = true
			s.runningMu.Unlock()

			s.addLog(fmt.Sprintf("⏰ [自动测试] 到达触发时间，开始执行定时任务 [%s]...", task.Name))
			
			// 开启协程执行任务并更新下次执行时间
			go func(t db.AutoTriggerTask) {
				defer func() {
					s.runningMu.Lock()
					delete(s.runningJobs, t.ID)
					s.runningMu.Unlock()
				}()

				s.runTask(t, "")

				// 更新数据库中下一次执行时间
				nextTime := time.Now().Add(time.Duration(t.IntervalSeconds) * time.Second)
				_ = db.UpdateNextTriggerTime(t.ID, nextTime)
			}(task)
		}
	}
}

// runTask runs a task. If filterAccountID is specified, only that account is triggered.
func (s *Scheduler) runTask(task db.AutoTriggerTask, filterAccountID string) {
	var targetAccountIDs []string
	if filterAccountID != "" {
		targetAccountIDs = []string{filterAccountID}
	} else {
		targetAccountIDs = task.AccountIDs
	}

	if len(targetAccountIDs) == 0 {
		return
	}

	var wg sync.WaitGroup
	dispatchedIdx := 0
	for _, accID := range targetAccountIDs {
		if s.accountMgr == nil {
			continue
		}
		acc := s.accountMgr.GetAccountByID(accID)
		if acc == nil {
			continue
		}

		modelsToTrigger := task.GetModelsForAccount(acc.ID)
		if len(modelsToTrigger) == 0 {
			continue
		}

		staggerDelay := time.Duration(0)
		if dispatchedIdx > 0 {
			staggerDelay = time.Duration(dispatchedIdx*250+100+rand.Intn(400)) * time.Millisecond
		}
		dispatchedIdx++

		wg.Add(1)
		go func(targetAcc *account.Account, models []string, delay time.Duration) {
			defer wg.Done()
			if delay > 0 {
				time.Sleep(delay)
			}

			for modelIdx, model := range models {
				if modelIdx > 0 {
					time.Sleep(time.Duration(600+rand.Intn(600)) * time.Millisecond)
				}
				// 安全防护：若账号已明确无此模型能力（如无 Argon 桶、未开通受限模型、或处于 404 负向学习屏蔽期），主动跳过规避上游风控
				nowMs := time.Now().UnixNano() / int64(time.Millisecond)
				isBlocked := targetAcc.IsModelMarkedUnsupported(model, nowMs)
				known, supported := targetAcc.ExplicitlySupportsModel(model)
				hasNoArgonBucket := strings.Contains(strings.ToLower(model), "argon") && targetAcc.HasArgonBucket != nil && !*targetAcc.HasArgonBucket

				if isBlocked || (known && !supported) || hasNoArgonBucket {
					s.addLog(fmt.Sprintf("⚠️ [自动测试] 任务 [%s] 账号 %s 暂不支持模型 %s，已自动跳过以规避风控", task.Name, targetAcc.Email, model))
					continue
				}

				s.addLog(fmt.Sprintf("⚡ [自动测试] 任务 [%s] 账号 %s 正在尝试自动触发模型 %s...", task.Name, targetAcc.Email, model))

				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				var getStoredProject func(string) string
				if s.quotaSvc != nil {
					getStoredProject = s.quotaSvc.GetStoredProject
				}
				var refreshToken func(*account.Account) (string, error)
				if s.authMgr != nil {
					refreshToken = s.authMgr.RefreshToken
				}
				var getUserAgent func() string
				if s.accountMgr != nil {
					getUserAgent = s.accountMgr.GetAntigravityUserAgent
				}

				respText, err := account.TriggerTestResponse(
					ctx,
					targetAcc,
					model,
					task.Prompt,
					getStoredProject,
					refreshToken,
					getUserAgent,
				)
				cancel()

				status := "success"
				message := respText
				if err != nil {
					status = "failed"
					message = err.Error()
					if strings.Contains(message, "HTTP error 404") && s.accountMgr != nil {
						s.accountMgr.MarkAccountModelUnsupported(targetAcc.ID, model, 24*time.Hour)
					}
					s.addLog(fmt.Sprintf("❌ [自动测试] 任务 [%s] 账号 %s 触发模型 %s 失败: %v", task.Name, targetAcc.Email, model, err))
				} else {
					s.addLog(fmt.Sprintf("✅ [自动测试] 任务 [%s] 账号 %s 触发模型 %s 成功！响应: %s", task.Name, targetAcc.Email, model, respText))
				}

				// 保存历史记录到数据库
				history := &db.TriggerHistory{
					TaskID:       task.ID,
					TaskName:     task.Name,
					TriggerType:  task.TriggerType,
					AccountEmail: targetAcc.Email,
					ModelName:    model,
					Status:       status,
					Message:      message,
					TriggerTime:  time.Now(),
				}
				if dbErr := db.SaveTriggerHistory(history); dbErr != nil {
					s.addLog(fmt.Sprintf("⚠️ [自动测试] 保存触发历史失败: %v", dbErr))
				}
			}
		}(acc, modelsToTrigger, staggerDelay)
	}

	wg.Wait()
	s.addLog(fmt.Sprintf("🏁 [自动测试] 任务 [%s] 自动化测试批处理执行完毕！", task.Name))
}

// TriggerTaskNow 立即手动触发指定的自动化任务包（异步非阻塞执行）
func (s *Scheduler) TriggerTaskNow(taskID int64) error {
	task, err := db.GetAutoTriggerTask(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task not found")
	}

	s.runningMu.Lock()
	if s.runningJobs[task.ID] {
		s.runningMu.Unlock()
		return fmt.Errorf("task is already running")
	}
	s.runningJobs[task.ID] = true
	s.runningMu.Unlock()

	s.addLog(fmt.Sprintf("⚡ [手动测试] 用户发起手动触发任务 [%s]...", task.Name))

	go func(t db.AutoTriggerTask) {
		defer func() {
			s.runningMu.Lock()
			delete(s.runningJobs, t.ID)
			s.runningMu.Unlock()
		}()

		s.runTask(t, "")
	}(*task)

	return nil
}

