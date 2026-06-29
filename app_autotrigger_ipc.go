package main

import (
	"encoding/json"
	"fmt"
	"time"

	"antigravity-proxy/internal/db"
)

// handleAutoTriggerIPC 处理自动化测试相关的 IPC 呼叫
func (a *App) handleAutoTriggerIPC(channel string, args []interface{}) (string, bool, error) {
	marshalResponse := func(val interface{}) (string, error) {
		b, err := json.Marshal(val)
		if err != nil {
			return `{"success":false,"error":"JSON serialization error"}`, nil
		}
		return string(b), nil
	}

	switch channel {
	case "autotrigger:list":
		tasks, err := db.ListAutoTriggerTasks()
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		data, _ := marshalResponse(map[string]interface{}{"success": true, "tasks": tasks})
		return data, true, nil

	case "autotrigger:save":
		var payload struct {
			ID              int64    `json:"id"`
			Name            string   `json:"name"`
			AccountIDs      []string `json:"accountIds"`
			ModelNames      []string `json:"modelNames"`
			Prompt          string   `json:"prompt"`
			TriggerType     string   `json:"triggerType"`
			IntervalSeconds int      `json:"intervalSeconds"`
			Enabled         bool     `json:"enabled"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		if payload.Name == "" {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "任务包名称不能为空"})
			return data, true, nil
		}
		if len(payload.AccountIDs) == 0 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "请至少选择一个账号"})
			return data, true, nil
		}
		if len(payload.ModelNames) == 0 {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": "请至少选择一个模型"})
			return data, true, nil
		}

		task := db.AutoTriggerTask{
			ID:              payload.ID,
			Name:            payload.Name,
			AccountIDs:      payload.AccountIDs,
			ModelNames:      payload.ModelNames,
			Prompt:          payload.Prompt,
			TriggerType:     payload.TriggerType,
			IntervalSeconds: payload.IntervalSeconds,
			Enabled:         payload.Enabled,
		}

		// 如果是定时任务且启用了，我们初始化下次执行时间为当前时间加上配置的间隔时间
		if task.TriggerType == "timer" && task.Enabled {
			t := time.Now().Add(time.Duration(task.IntervalSeconds) * time.Second)
			task.NextTriggerTime = &t
		}

		err := db.SaveAutoTriggerTask(&task)
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}

		a.AddLog(fmt.Sprintf("⏰ [任务配置] 成功保存自动化触发任务包 [%s]", task.Name))
		data, _ := marshalResponse(map[string]interface{}{"success": true, "task": task})
		return data, true, nil

	case "autotrigger:delete":
		var payload struct {
			ID int64 `json:"id"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		err := db.DeleteAutoTriggerTask(payload.ID)
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}
		a.AddLog(fmt.Sprintf("⏰ [任务配置] 成功删除自动化触发任务 ID %d", payload.ID))
		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil

	case "autotrigger:toggle":
		var payload struct {
			ID      int64 `json:"id"`
			Enabled bool  `json:"enabled"`
		}
		if len(args) > 0 {
			bytesPayload, _ := json.Marshal(args[0])
			_ = json.Unmarshal(bytesPayload, &payload)
		}

		err := db.ToggleAutoTriggerTask(payload.ID, payload.Enabled)
		if err != nil {
			data, _ := marshalResponse(map[string]interface{}{"success": false, "error": err.Error()})
			return data, true, nil
		}

		if payload.Enabled {
			// 若开启，重设它的定时任务下次触发时间为当前加间隔时间
			tasks, errList := db.ListAutoTriggerTasks()
			if errList == nil {
				for _, t := range tasks {
					if t.ID == payload.ID && t.TriggerType == "timer" {
						next := time.Now().Add(time.Duration(t.IntervalSeconds) * time.Second)
						_ = db.UpdateNextTriggerTime(t.ID, next)
					}
				}
			}
			a.AddLog(fmt.Sprintf("⏰ [任务配置] 成功开启自动化任务 ID %d", payload.ID))
		} else {
			a.AddLog(fmt.Sprintf("⏰ [任务配置] 成功停用自动化任务 ID %d", payload.ID))
		}

		data, _ := marshalResponse(map[string]interface{}{"success": true})
		return data, true, nil
	}

	return "", false, nil
}
