package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/service"
)

func loginRateLimit(rdb *goredis.Client, perMinute int) gin.HandlerFunc {
	if perMinute <= 0 {
		perMinute = 10
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		key := fmt.Sprintf("ff:login:rl:%s", c.ClientIP())
		count, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			c.Next()
			return
		}
		if count == 1 {
			rdb.Expire(ctx, key, time.Minute)
		}
		if count > int64(perMinute) {
			ttl, _ := rdb.TTL(ctx, key).Result()
			seconds := int(ttl.Seconds())
			if seconds < 1 {
				seconds = 60
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			abortError(c, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak percobaan login, coba lagi nanti")
			return
		}
		c.Next()
	}
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Writer.Header().Set("X-Request-ID", id)
		c.Next()
	}
}

func loggingMiddleware(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		reqID, _ := c.Get("request_id")
		log.Info("http request",
			"request_id", reqID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}

func recoveryMiddleware(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "panic", r, "path", c.Request.URL.Path)
				abortError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
			}
		}()
		c.Next()
	}
}

func corsMiddleware(origins []string) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAll = true
			continue
		}
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if allowAll {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Vary", "Origin")
			}
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		observability.HTTPRequests.WithLabelValues(c.Request.Method, path, strconv.Itoa(c.Writer.Status())).Inc()
	}
}

func authMiddleware(auth *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			abortError(c, http.StatusUnauthorized, "unauthorized", "Token autentikasi diperlukan")
			return
		}
		raw := strings.TrimSpace(header[7:])
		claims, err := auth.ParseAccess(raw)
		if err != nil {
			abortError(c, http.StatusUnauthorized, "invalid_token", "Token autentikasi tidak valid atau kedaluwarsa")
			return
		}
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			abortError(c, http.StatusUnauthorized, "invalid_token", "Token autentikasi tidak valid")
			return
		}
		role := domain.UserRole(claims.Role)
		if !domain.ValidRole(role) {
			abortError(c, http.StatusUnauthorized, "invalid_token", "Role pada token tidak valid")
			return
		}
		setAuth(c, userID, role)
		c.Next()
	}
}

func requireRoles(roles ...domain.UserRole) gin.HandlerFunc {
	allowed := make(map[domain.UserRole]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(c *gin.Context) {
		_, role, ok := currentUser(c)
		if !ok {
			abortError(c, http.StatusUnauthorized, "unauthorized", "Autentikasi diperlukan")
			return
		}
		if _, ok := allowed[role]; !ok {
			abortError(c, http.StatusForbidden, "forbidden", "Anda tidak memiliki akses untuk aksi ini")
			return
		}
		c.Next()
	}
}
