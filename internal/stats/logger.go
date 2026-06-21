package stats

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"` // "RETRY" | "ERROR"
	Path      string `json:"path"`
	Model     string `json:"model"`
	Account   string `json:"account"`
	Attempt   int    `json:"attempt"`
	Error     string `json:"error"`
}

type RetryErrorLogger struct {
	sync.RWMutex
	persistPath     string
	logs            []*LogEntry
	maxLogs         int
	onPayloadUpdate func()
}

func NewRetryErrorLogger() *RetryErrorLogger {
	return &RetryErrorLogger{
		logs:    make([]*LogEntry, 0),
		maxLogs: 300,
	}
}

func (l *RetryErrorLogger) Init(userDataPath string) {
	l.Lock()
	l.persistPath = filepath.Join(userDataPath, "retry_error_logs.json")
	l.Unlock()

	l.LoadFromDisk()
}

func (l *RetryErrorLogger) UpdatePath(newPath string) {
	l.SaveToDisk()

	l.Lock()
	l.persistPath = filepath.Join(newPath, "retry_error_logs.json")
	l.Unlock()

	l.LoadFromDisk()
}

func (l *RetryErrorLogger) SetOnPayloadUpdate(fn func()) {
	l.Lock()
	defer l.Unlock()
	l.onPayloadUpdate = fn
}

func (l *RetryErrorLogger) Log(logType string, reqPath, model, account string, attempt int, errorMsg string) {
	now := time.Now()
	timestamp := fmt.Sprintf("%02d/%02d %02d:%02d:%02d", now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())

	entry := &LogEntry{
		ID:        fmt.Sprintf("%d-%d", now.UnixNano(), rand.Intn(1000)),
		Timestamp: timestamp,
		Type:      logType,
		Path:      reqPath,
		Model:     model,
		Account:   account,
		Attempt:   attempt,
		Error:     errorMsg,
	}

	l.Lock()
	l.logs = append([]*LogEntry{entry}, l.logs...)
	if len(l.logs) > l.maxLogs {
		l.logs = l.logs[:l.maxLogs]
	}
	l.Unlock()

	l.SaveToDisk()

	l.RLock()
	callback := l.onPayloadUpdate
	l.RUnlock()
	if callback != nil {
		callback()
	}
}

func (l *RetryErrorLogger) GetLogs() []*LogEntry {
	l.RLock()
	defer l.RUnlock()

	// deep copy logs to avoid race conditions
	logsCopy := make([]*LogEntry, len(l.logs))
	for i, log := range l.logs {
		logsCopy[i] = &LogEntry{
			ID:        log.ID,
			Timestamp: log.Timestamp,
			Type:      log.Type,
			Path:      log.Path,
			Model:     log.Model,
			Account:   log.Account,
			Attempt:   log.Attempt,
			Error:     log.Error,
		}
	}
	return logsCopy
}

func (l *RetryErrorLogger) ClearLogs(logType string) {
	l.Lock()
	if logType == "" || logType == "ALL" {
		l.logs = make([]*LogEntry, 0)
	} else {
		var newLogs []*LogEntry
		for _, log := range l.logs {
			if log.Type != logType {
				newLogs = append(newLogs, log)
			}
		}
		l.logs = newLogs
	}
	l.Unlock()

	l.SaveToDisk()
}

func (l *RetryErrorLogger) SaveToDisk() {
	l.RLock()
	path := l.persistPath
	if path == "" {
		l.RUnlock()
		return
	}
	data := l.logs
	l.RUnlock()

	bytesData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Printf("[RetryErrorLogger] Failed to marshal logs: %v\n", err)
		return
	}

	err = os.WriteFile(path, bytesData, 0644)
	if err != nil {
		fmt.Printf("[RetryErrorLogger] Failed to write logs: %v\n", err)
	}
}

func (l *RetryErrorLogger) LoadFromDisk() {
	l.Lock()
	defer l.Unlock()

	if l.persistPath == "" {
		l.logs = make([]*LogEntry, 0)
		return
	}

	if _, err := os.Stat(l.persistPath); os.IsNotExist(err) {
		l.logs = make([]*LogEntry, 0)
		return
	}

	data, err := os.ReadFile(l.persistPath)
	if err != nil {
		l.logs = make([]*LogEntry, 0)
		return
	}

	var parsed []*LogEntry
	if err := json.Unmarshal(data, &parsed); err != nil {
		l.logs = make([]*LogEntry, 0)
		return
	}
	l.logs = parsed
}
