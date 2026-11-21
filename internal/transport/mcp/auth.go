package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/service"
)

type principalContextKey struct{}

func PrincipalFromContext(ctx context.Context) *service.MCPPrincipal {
	principal, _ := ctx.Value(principalContextKey{}).(*service.MCPPrincipal)
	return principal
}

func withPrincipal(ctx context.Context, principal *service.MCPPrincipal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// WithPrincipal attaches a principal to a connection context. The stdio
// transport uses it to make the configured identity available to tool
// handlers without an HTTP middleware.
func WithPrincipal(ctx context.Context, principal *service.MCPPrincipal) context.Context {
	return withPrincipal(ctx, principal)
}

// AuthMiddleware authenticates the Bearer token against the mcp_api_keys table
// and enforces a per-key request rate limit.
func AuthMiddleware(keys *service.MCPKeyService, rdb *goredis.Client, perMinute int, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if keys == nil {
				writeAuthError(w, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi", 5)
				return
			}
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				observability.MCPAuthFailures.WithLabelValues("missing_token").Inc()
				writeAuthError(w, http.StatusUnauthorized, "invalid_token", "API key tidak valid", 0)
				return
			}
			principal, err := keys.Authenticate(r.Context(), token)
			if err != nil {
				reason, status, message := classifyAuthError(err)
				observability.MCPAuthFailures.WithLabelValues(reason).Inc()
				if status >= http.StatusInternalServerError {
					log.Error("mcp authentication backend failed", "reason", reason, "error", err)
				} else {
					log.Debug("mcp authentication rejected", "reason", reason)
				}
				retryAfter := 0
				if status == http.StatusServiceUnavailable {
					retryAfter = 5
				}
				writeAuthError(w, status, reason, message, retryAfter)
				return
			}

			if perMinute > 0 && rdb != nil {
				allowed, retryAfter := allowMCPRequest(r.Context(), rdb, principal.KeyID.String(), perMinute)
				if !allowed {
					observability.MCPAuthFailures.WithLabelValues("rate_limited").Inc()
					writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak permintaan MCP, coba lagi nanti", retryAfter)
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
		})
	}
}

func classifyAuthError(err error) (reason string, status int, message string) {
	switch {
	case errors.Is(err, service.ErrMCPKeyInactive):
		return "inactive_key", http.StatusUnauthorized, "API key tidak valid"
	case errors.Is(err, service.ErrMCPKeyExpired):
		return "expired_key", http.StatusUnauthorized, "API key tidak valid"
	case errors.Is(err, service.ErrMCPKeyInvalid):
		return "invalid_token", http.StatusUnauthorized, "API key tidak valid"
	default:
		return "backend_error", http.StatusServiceUnavailable, "Layanan autentikasi MCP sedang tidak tersedia"
	}
}

func bearerToken(raw string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(raw), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func allowMCPRequest(ctx context.Context, rdb *goredis.Client, keyID string, perMinute int) (bool, int) {
	now := time.Now()
	window := now.Unix() / 60
	redisKey := fmt.Sprintf("ff:mcp:rl:%s:%d", keyID, window)
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
