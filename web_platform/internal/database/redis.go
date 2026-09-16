package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"antigravity-web-platform/internal/config"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client

// InitRedis 初始化 Redis 客户端连接池并探测心跳
func InitRedis(cfg *config.Config) (*redis.Client, error) {
	if !cfg.Redis.Enabled {
		log.Println("ℹ️ [Redis] 配置中已停用 Redis，会话将降级至纯 JWT 状态模式")
		return nil, nil
	}

	addr := fmt.Sprintf("%s:%s", cfg.Redis.Host, cfg.Redis.Port)
	log.Printf("[Redis] 正在连接远程 Redis 服务 (%s, 选用 DB %d)...", addr, cfg.Redis.DB)

	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pong, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Printf("⚠️ [Redis] 连接远程 Redis 失败: %v", err)
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	log.Printf("✅ [Redis] 远程 Redis 服务连接成功！(回执: %s, 隔离数据库: DB %d)", pong, cfg.Redis.DB)
	RDB = rdb
	return rdb, nil
}

// IsRedisAvailable 判断当前全局 Redis 实例是否已就绪
func IsRedisAvailable() bool {
	return RDB != nil
}
