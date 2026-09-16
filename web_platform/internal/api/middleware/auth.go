package middleware

import (
	"net/http"
	"strings"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/database"
	"antigravity-web-platform/internal/pkg/jwt"
	"antigravity-web-platform/internal/pkg/response"
	"antigravity-web-platform/internal/service"

	"github.com/gin-gonic/gin"
)

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.FailWithStatus(c, http.StatusUnauthorized, 401, "请先登录")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.FailWithStatus(c, http.StatusUnauthorized, 401, "认证格式不正确")
			c.Abort()
			return
		}

		rawToken := strings.TrimSpace(parts[1])
		cfg := config.GlobalConfig
		claims, err := jwt.ParseToken(rawToken, cfg.Server.JWTSecret)
		if err != nil {
			response.FailWithStatus(c, http.StatusUnauthorized, 401, "登录已过期或凭证无效")
			c.Abort()
			return
		}

		// 若已启用远端 Redis 会话管理，校验 DB 1 会话有效性（防止异地登出/密码变更后伪造旧会话）
		if database.IsRedisAvailable() {
			sessionSvc := service.NewSessionService()
			session, err := sessionSvc.GetSession(c.Request.Context(), rawToken)
			if err != nil || session == nil {
				response.FailWithStatus(c, http.StatusUnauthorized, 401, "登录会话已失效或已登出，请重新登录")
				c.Abort()
				return
			}
			if session.Status == "disabled" {
				response.FailWithStatus(c, http.StatusForbidden, 403, "该账号已被禁用，请联系管理员")
				c.Abort()
				return
			}
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		if role != "admin" {
			response.FailWithStatus(c, http.StatusForbidden, 403, "权限不足，仅管理员可访问")
			c.Abort()
			return
		}
		c.Next()
	}
}
