package db

import (
	"fmt"
	"time"
)

type TriggerHistory struct {
	ID           int64     `json:"id"`
	TaskID       int64     `json:"taskId"`
	TaskName     string    `json:"taskName"`
	TriggerType  string    `json:"triggerType"`
	AccountEmail string    `json:"accountEmail"`
	ModelName    string    `json:"modelName"`
	Status       string    `json:"status"` // "success" or "failed"
	Message      string    `json:"message"`
	TriggerTime  time.Time `json:"triggerTime"`
}

// SaveTriggerHistory inserts a new trigger history record
func SaveTriggerHistory(h *TriggerHistory) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `INSERT INTO auto_trigger_history (task_id, task_name, trigger_type, account_email, model_name, status, message, trigger_time)
              VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	
	timeStr := h.TriggerTime.Format(time.RFC3339)
	_, err := GlobalDB.Exec(query, h.TaskID, h.TaskName, h.TriggerType, h.AccountEmail, h.ModelName, h.Status, h.Message, timeStr)
	return err
}

// ListTriggerHistory queries history records with pagination
func ListTriggerHistory(limit, offset int) ([]TriggerHistory, int, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return nil, 0, fmt.Errorf("database not initialized")
	}

	// Get total count
	var total int
	err := GlobalDB.QueryRow("SELECT COUNT(*) FROM auto_trigger_history").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get paginated list, ordered by trigger_time desc, then id desc
	rows, err := GlobalDB.Query("SELECT id, task_id, task_name, trigger_type, account_email, model_name, status, message, trigger_time FROM auto_trigger_history ORDER BY trigger_time DESC, id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	histories := []TriggerHistory{}
	for rows.Next() {
		var h TriggerHistory
		var timeStr string
		err := rows.Scan(&h.ID, &h.TaskID, &h.TaskName, &h.TriggerType, &h.AccountEmail, &h.ModelName, &h.Status, &h.Message, &timeStr)
		if err != nil {
			return nil, 0, err
		}

		if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
			h.TriggerTime = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", timeStr); err == nil {
			h.TriggerTime = t
		}
		histories = append(histories, h)
	}

	return histories, total, nil
}

// ClearTriggerHistory clears all trigger history
func ClearTriggerHistory() error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	_, err := GlobalDB.Exec("DELETE FROM auto_trigger_history")
	return err
}
