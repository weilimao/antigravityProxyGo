package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/model"
)

const (
	// Redis Key 前缀命名规范
	KeyPrefixSessionToken = "session:token:"
	KeyPrefixUserTokens   = "user:tokens:"
)

type SessionData struct {
	UserID       uint      `json:"userId"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	PlanID       *uint     `json:"planId,omitempty"`
	PlanExpireAt int64     `json:"planExpireAt"`
	LoginAt      time.Time `json:"loginAt"`
}

type SessionService struct{}

func NewSessionService() *SessionService {
	return &SessionService{}
}

// SaveSession 将登录 Token 和用户会话缓存写入 Redis（选用 DB 1）
func (s *SessionService) SaveSession(ctx context.Context, token string, user *model.User, duration time.Duration) error {
	if !database.IsRedisAvailable() {
		return nil
	}

	sessionData := SessionData{
		UserID:       user.ID,
		Username:     user.Username,
		Email:        user.Email,
		Role:         user.Role,
		Status:       user.Status,
		PlanID:       user.PlanID,
		PlanExpireAt: user.PlanExpireAt,
		LoginAt:      time.Now(),
	}

	dataBytes, err := json.Marshal(sessionData)
	if err != nil {
		return fmt.Errorf("marshal session data failed: %w", err)
	}

	tokenKey := KeyPrefixSessionToken + token
	userKey := fmt.Sprintf("%s%d", KeyPrefixUserTokens, user.ID)

	pipe := database.RDB.Pipeline()
	// 1. 设置 Token 映射的用户会话数据与生命周期 TTL
	pipe.Set(ctx, tokenKey, string(dataBytes), duration)
	// 2. 将该 Token 记录进该用户的在线设备集合
	pipe.SAdd(ctx, userKey, token)
	pipe.Expire(ctx, userKey, duration+24*time.Hour)

	_, err = pipe.Exec(ctx)
	if err != nil {
		log.Printf("⚠️ [Session] 写入 Redis 会话失败: %v", err)
		return err
	}

	log.Printf("🔑 [Session] 用户 [%s (ID:%d)] 登录会话已成功写入 Redis DB 1 (TTL: %v)", user.Username, user.ID, duration)
	return nil
}

// GetSession 根据 Token 亚毫秒级读取活跃会话
func (s *SessionService) GetSession(ctx context.Context, token string) (*SessionData, error) {
	if !database.IsRedisAvailable() {
		return nil, nil
	}

	tokenKey := KeyPrefixSessionToken + token
	val, err := database.RDB.Get(ctx, tokenKey).Result()
	if err != nil {
		return nil, err
	}

	var session SessionData
	if err := json.Unmarshal([]byte(val), &session); err != nil {
		return nil, err
	}

	return &session, nil
}

// DeleteSession 主动登出时销毁指定的 Token 会话
func (s *SessionService) DeleteSession(ctx context.Context, token string) error {
	if !database.IsRedisAvailable() {
		return nil
	}

	session, _ := s.GetSession(ctx, token)

	tokenKey := KeyPrefixSessionToken + token
	pipe := database.RDB.Pipeline()
	pipe.Del(ctx, tokenKey)
	if session != nil {
		userKey := fmt.Sprintf("%s%d", KeyPrefixUserTokens, session.UserID)
		pipe.SRem(ctx, userKey, token)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Printf("⚠️ [Session] 删除 Redis 会话失败: %v", err)
		return err
	}

	log.Printf("🚪 [Session] 会话已从 Redis DB 1 成功销毁: %s...", token[:min(10, len(token))])
	return nil
}

// InvalidateUserSessions 一键销毁指定用户的所有在线 Token（修改密码或账号封禁时调用）
func (s *SessionService) InvalidateUserSessions(ctx context.Context, userID uint) error {
	if !database.IsRedisAvailable() {
		return nil
	}

	userKey := fmt.Sprintf("%s%d", KeyPrefixUserTokens, userID)
	tokens, err := database.RDB.SMembers(ctx, userKey).Result()
	if err != nil {
		return err
	}

	if len(tokens) == 0 {
		return nil
	}

	pipe := database.RDB.Pipeline()
	for _, tok := range tokens {
		pipe.Del(ctx, KeyPrefixSessionToken+tok)
	}
	pipe.Del(ctx, userKey)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return err
	}

	log.Printf("🧹 [Session] 已一键清理用户 ID %d 的所有 Redis 活跃会话 (%d 个)", userID, len(tokens))
	return nil
}
