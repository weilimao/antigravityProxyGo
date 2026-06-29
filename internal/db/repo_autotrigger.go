package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AutoTriggerTask struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	AccountIDs      []string   `json:"accountIds"`
	ModelNames      []string   `json:"modelNames"`
	Prompt          string     `json:"prompt"`
	TriggerType     string     `json:"triggerType"` // "timer" or "quota_refreshed"
	IntervalSeconds int        `json:"intervalSeconds"`
	NextTriggerTime *time.Time `json:"nextTriggerTime"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// SaveAutoTriggerTask inserts a new task or updates an existing one
func SaveAutoTriggerTask(task *AutoTriggerTask) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	accIDsBytes, _ := json.Marshal(task.AccountIDs)
	modNamesBytes, _ := json.Marshal(task.ModelNames)

	var nextTimeStr sql.NullString
	if task.NextTriggerTime != nil {
		nextTimeStr.Valid = true
		nextTimeStr.String = task.NextTriggerTime.Format(time.RFC3339)
	}

	enabledInt := 0
	if task.Enabled {
		enabledInt = 1
	}

	if task.ID == 0 {
		// Insert
		query := `INSERT INTO auto_trigger_tasks (name, account_ids, model_names, prompt, trigger_type, interval_seconds, next_trigger_time, enabled)
                  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		res, err := GlobalDB.Exec(query, task.Name, string(accIDsBytes), string(modNamesBytes), task.Prompt, task.TriggerType, task.IntervalSeconds, nextTimeStr, enabledInt)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err == nil {
			task.ID = id
		}
	} else {
		// Update
		query := `UPDATE auto_trigger_tasks SET name = ?, account_ids = ?, model_names = ?, prompt = ?, trigger_type = ?, interval_seconds = ?, next_trigger_time = ?, enabled = ?
                  WHERE id = ?`
		_, err := GlobalDB.Exec(query, task.Name, string(accIDsBytes), string(modNamesBytes), task.Prompt, task.TriggerType, task.IntervalSeconds, nextTimeStr, enabledInt, task.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteAutoTriggerTask deletes a task by ID
func DeleteAutoTriggerTask(id int64) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := GlobalDB.Exec("DELETE FROM auto_trigger_tasks WHERE id = ?", id)
	return err
}

// ListAutoTriggerTasks lists all tasks
func ListAutoTriggerTasks() ([]AutoTriggerTask, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := GlobalDB.Query("SELECT id, name, account_ids, model_names, prompt, trigger_type, interval_seconds, next_trigger_time, enabled, created_at FROM auto_trigger_tasks ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []AutoTriggerTask
	for rows.Next() {
		var task AutoTriggerTask
		var accIDsStr, modNamesStr, createdAtStr string
		var nextTimeStr sql.NullString
		var enabledInt int

		err := rows.Scan(&task.ID, &task.Name, &accIDsStr, &modNamesStr, &task.Prompt, &task.TriggerType, &task.IntervalSeconds, &nextTimeStr, &enabledInt, &createdAtStr)
		if err != nil {
			return nil, err
		}

		_ = json.Unmarshal([]byte(accIDsStr), &task.AccountIDs)
		_ = json.Unmarshal([]byte(modNamesStr), &task.ModelNames)
		task.Enabled = enabledInt == 1

		if nextTimeStr.Valid && nextTimeStr.String != "" {
			if t, err := time.Parse(time.RFC3339, nextTimeStr.String); err == nil {
				task.NextTriggerTime = &t
			}
		}

		if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
			task.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			task.CreatedAt = t
		}

		tasks = append(tasks, task)
	}
	return tasks, nil
}

// UpdateNextTriggerTime updates only the next trigger time of a task
func UpdateNextTriggerTime(id int64, nextTime time.Time) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	timeStr := nextTime.Format(time.RFC3339)
	_, err := GlobalDB.Exec("UPDATE auto_trigger_tasks SET next_trigger_time = ? WHERE id = ?", timeStr, id)
	return err
}

// ToggleAutoTriggerTask enables/disables a task
func ToggleAutoTriggerTask(id int64, enabled bool) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	_, err := GlobalDB.Exec("UPDATE auto_trigger_tasks SET enabled = ? WHERE id = ?", enabledInt, id)
	return err
}
