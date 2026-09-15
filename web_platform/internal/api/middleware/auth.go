package middleware

import (
	"net/http"
	"strings"

	"antigravity-web-platform/internal/config"
	"antigravity-web-platform/internal/pkg/jwt"
	"antigravity-web-platform/internal/pkg/response"

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

		cfg := config.GlobalConfig
		claims, err := jwt.ParseToken(parts[1], cfg.Server.JWTSecret)
		if err != nil {
			response.FailWithStatus(c, http.StatusUnauthorized, 401, "登录已过期或凭证无效")
			c.Abort()
			return
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
