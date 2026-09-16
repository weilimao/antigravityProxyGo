package relay

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
)

// UsageRecord 封装单次 API Key 用量消费事件
type UsageRecord struct {
	UserID    string
	APIKeyID  string
	KeyStr    string
	Family    APIKeyFamily
	Tokens    int64
	Timestamp time.Time
}

// UserUsageStore 定义用户与 API Key 用量持久化存储抽象接口
type UserUsageStore interface {
	RecordUsage(record UsageRecord)
	Flush() error
	Close() error
	IsDatabaseMode() bool
}

// NewUserUsageStore 构造函数：若注入了 GORM 数据库实例则使用 DB 模式，否则回退为防抖文件模式
func NewUserUsageStore(db *gorm.DB, fileFlushFn func()) UserUsageStore {
	if db != nil {
		return NewDBUserUsageStore(db)
	}
	return NewFileUserUsageStore(fileFlushFn)
}

// ==================== DB 模式：异步批量落库 ====================

type aggregatedUsage struct {
	tokens        int64
	geminiTokens  int64
	claudeTokens  int64
	nvidiaTokens  int64
	grokTokens    int64
	lastTimestamp time.Time
}

type flushReq struct {
	done chan struct{}
}

type DBUserUsageStore struct {
	db        *gorm.DB
	queue     chan UsageRecord
	flushChan chan flushReq
	closeChan chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	pending   map[string]*aggregatedUsage // 仅由 workerLoop 协程访问，天然并发安全
}

func NewDBUserUsageStore(db *gorm.DB) *DBUserUsageStore {
	s := &DBUserUsageStore{
		db:        db,
		queue:     make(chan UsageRecord, 10000),
		flushChan: make(chan flushReq),
		closeChan: make(chan struct{}),
		pending:   make(map[string]*aggregatedUsage),
	}

	s.wg.Add(1)
	go s.workerLoop()
	return s
}

func (s *DBUserUsageStore) IsDatabaseMode() bool {
	return true
}

func (s *DBUserUsageStore) RecordUsage(record UsageRecord) {
	if record.Tokens <= 0 || (record.KeyStr == "" && record.APIKeyID == "") {
		return
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}

	select {
	case s.queue <- record:
	case <-s.closeChan:
	default:
		// 极端高压缓冲队列满时，非阻塞异步执行单条扣减
		go s.applySingleRecord(record)
	}
}

func (s *DBUserUsageStore) workerLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case record := <-s.queue:
			s.accumulate(record)
			if len(s.pending) >= 50 {
				s.flushPending()
			}

		case req := <-s.flushChan:
			// 收到显式 Flush 请求，先排空队列中积攒的所有记录
			for {
				select {
				case r := <-s.queue:
					s.accumulate(r)
				default:
					goto DRAINED
				}
			}
		DRAINED:
			s.flushPending()
			close(req.done)

		case <-ticker.C:
			if len(s.pending) > 0 {
				s.flushPending()
			}

		case <-s.closeChan:
			for {
				select {
				case r := <-s.queue:
					s.accumulate(r)
				default:
					s.flushPending()
					return
				}
			}
		}
	}
}

func (s *DBUserUsageStore) accumulate(r UsageRecord) {
	lookupKey := r.KeyStr
	if lookupKey == "" {
		lookupKey = r.APIKeyID
	}
	if lookupKey == "" {
		return
	}

	item, exists := s.pending[lookupKey]
	if !exists {
		item = &aggregatedUsage{
			lastTimestamp: r.Timestamp,
		}
		s.pending[lookupKey] = item
	}

	item.tokens += r.Tokens
	switch r.Family {
	case FamilyClaude:
		item.claudeTokens += r.Tokens
	case FamilyNvidia:
		item.nvidiaTokens += r.Tokens
	case FamilyGrok:
		item.grokTokens += r.Tokens
	default:
		item.geminiTokens += r.Tokens
	}
	if r.Timestamp.After(item.lastTimestamp) {
		item.lastTimestamp = r.Timestamp
	}
}

func (s *DBUserUsageStore) flushPending() {
	if len(s.pending) == 0 || s.db == nil {
		return
	}

	for keyStr, usage := range s.pending {
		if usage.tokens <= 0 {
			continue
		}

		idVal, _ := strconv.ParseUint(keyStr, 10, 64)
		now := time.Now()
		var err error
		if idVal > 0 {
			sqlQuery := `
				UPDATE api_keys 
				SET used_tokens = used_tokens + ?,
				    used_gemini_tokens = used_gemini_tokens + ?,
				    used_claude_tokens = used_claude_tokens + ?,
				    used_nvidia_tokens = used_nvidia_tokens + ?,
				    used_grok_tokens = used_grok_tokens + ?,
				    last_used_at = ?,
				    updated_at = ?
				WHERE ` + "`key`" + ` = ? OR id = ?
			`
			err = s.db.Exec(sqlQuery,
				usage.tokens,
				usage.geminiTokens,
				usage.claudeTokens,
				usage.nvidiaTokens,
				usage.grokTokens,
				usage.lastTimestamp,
				now,
				keyStr,
				idVal,
			).Error
		} else {
			sqlQuery := `
				UPDATE api_keys 
				SET used_tokens = used_tokens + ?,
				    used_gemini_tokens = used_gemini_tokens + ?,
				    used_claude_tokens = used_claude_tokens + ?,
				    used_nvidia_tokens = used_nvidia_tokens + ?,
				    used_grok_tokens = used_grok_tokens + ?,
				    last_used_at = ?,
				    updated_at = ?
				WHERE ` + "`key`" + ` = ?
			`
			err = s.db.Exec(sqlQuery,
				usage.tokens,
				usage.geminiTokens,
				usage.claudeTokens,
				usage.nvidiaTokens,
				usage.grokTokens,
				usage.lastTimestamp,
				now,
				keyStr,
			).Error
		}

		if err != nil {
			fmt.Printf("⚠️ [DBUserUsageStore] Failed to update token usage for key %s: %v\n", keyStr, err)
		}
	}

	// 清空当前批次
	s.pending = make(map[string]*aggregatedUsage)
}

func (s *DBUserUsageStore) applySingleRecord(r UsageRecord) {
	lookupKey := r.KeyStr
	if lookupKey == "" {
		lookupKey = r.APIKeyID
	}
	if lookupKey == "" || s.db == nil {
		return
	}

	var gemini, claude, nvidia, grok int64
	switch r.Family {
	case FamilyClaude:
		claude = r.Tokens
	case FamilyNvidia:
		nvidia = r.Tokens
	case FamilyGrok:
		grok = r.Tokens
	default:
		gemini = r.Tokens
	}

	idVal, _ := strconv.ParseUint(lookupKey, 10, 64)
	now := time.Now()
	if idVal > 0 {
		sqlQuery := `
			UPDATE api_keys 
			SET used_tokens = used_tokens + ?,
			    used_gemini_tokens = used_gemini_tokens + ?,
			    used_claude_tokens = used_claude_tokens + ?,
			    used_nvidia_tokens = used_nvidia_tokens + ?,
			    used_grok_tokens = used_grok_tokens + ?,
			    last_used_at = ?,
			    updated_at = ?
			WHERE ` + "`key`" + ` = ? OR id = ?
		`
		_ = s.db.Exec(sqlQuery, r.Tokens, gemini, claude, nvidia, grok, r.Timestamp, now, lookupKey, idVal).Error
	} else {
		sqlQuery := `
			UPDATE api_keys 
			SET used_tokens = used_tokens + ?,
			    used_gemini_tokens = used_gemini_tokens + ?,
			    used_claude_tokens = used_claude_tokens + ?,
			    used_nvidia_tokens = used_nvidia_tokens + ?,
			    used_grok_tokens = used_grok_tokens + ?,
			    last_used_at = ?,
			    updated_at = ?
			WHERE ` + "`key`" + ` = ?
		`
		_ = s.db.Exec(sqlQuery, r.Tokens, gemini, claude, nvidia, grok, r.Timestamp, now, lookupKey).Error
	}
}

func (s *DBUserUsageStore) Flush() error {
	req := flushReq{done: make(chan struct{})}
	select {
	case s.flushChan <- req:
		<-req.done
		return nil
	case <-s.closeChan:
		return nil
	}
}

func (s *DBUserUsageStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeChan)
	})
	s.wg.Wait()
	return nil
}

// ==================== 文件模式：防抖批量落盘 ====================

type FileUserUsageStore struct {
	flushFn   func()
	dirty     bool
	mu        sync.Mutex
	ticker    *time.Ticker
	closeChan chan struct{}
	wg        sync.WaitGroup
}

func NewFileUserUsageStore(flushFn func()) *FileUserUsageStore {
	s := &FileUserUsageStore{
		flushFn:   flushFn,
		ticker:    time.NewTicker(3 * time.Second),
		closeChan: make(chan struct{}),
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.ticker.C:
				s.mu.Lock()
				if s.dirty && s.flushFn != nil {
					s.flushFn()
					s.dirty = false
				}
				s.mu.Unlock()
			case <-s.closeChan:
				s.mu.Lock()
				if s.dirty && s.flushFn != nil {
					s.flushFn()
					s.dirty = false
				}
				s.mu.Unlock()
				return
			}
		}
	}()

	return s
}

func (s *FileUserUsageStore) IsDatabaseMode() bool {
	return false
}

func (s *FileUserUsageStore) RecordUsage(record UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
}

func (s *FileUserUsageStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty && s.flushFn != nil {
		s.flushFn()
		s.dirty = false
	}
	return nil
}

func (s *FileUserUsageStore) Close() error {
	s.mu.Lock()
	select {
	case <-s.closeChan:
		s.mu.Unlock()
		return nil
	default:
		s.ticker.Stop()
		close(s.closeChan)
	}
	s.mu.Unlock()

	s.wg.Wait()
	return s.Flush()
}
