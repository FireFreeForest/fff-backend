package mcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/observability"
)

// AuthMiddleware authenticates the Bearer token against the MCP_API_KEY
// environment value and enforces a shared request rate limit. MCP access is
// read-only, so there is no per-key identity or role to resolve.
func AuthMiddleware(token string, rdb *goredis.Client, perMinute int, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSpace(token) == "" {
				writeAuthError(w, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi", 5)
				return
			}
			provided, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok || !secureCompare(token, provided) {
				reason := "invalid_token"
				if !ok {
					reason = "missing_token"
				}
				observability.MCPAuthFailures.WithLabelValues(reason).Inc()
				writeAuthError(w, http.StatusUnauthorized, "invalid_token", "API key tidak valid", 0)
				return
			}

			if perMinute > 0 && rdb != nil {
				allowed, retryAfter := allowMCPRequest(r.Context(), rdb, "env", perMinute)
				if !allowed {
					observability.MCPAuthFailures.WithLabelValues("rate_limited").Inc()
					writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak permintaan MCP, coba lagi nanti", retryAfter)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func secureCompare(expected, provided string) bool {
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func bearerToken(raw string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(raw), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func allowMCPRequest(ctx context.Context, rdb *goredis.Client, scope string, perMinute int) (bool, int) {
	now := time.Now()
	window := now.Unix() / 60
	redisKey := fmt.Sprintf("ff:mcp:rl:%s:%d", scope, window)
	count, err := rdb.Incr(ctx, redisKey).Result()
	if err != nil {
		// Match the login rate limiter: a Redis outage must not take MCP down.
		return true, 0
	}
	rdb.Expire(ctx, redisKey, 2*time.Minute)
	if count > int64(perMinute) {
		retryAfter := int(60 - now.Unix()%60)
		if retryAfter < 1 {
			retryAfter = 60
		}
		return false, retryAfter
	}
	return true, 0
}

func writeAuthError(w http.ResponseWriter, status int, code, message string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
	}
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
