package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
)

// assistantScope selects which limit applies to a proxied assistant request.
type assistantScope int

const (
	// assistantScopeRead covers cheap, short requests such as listing threads or
	// reading messages. It is rate limited but never holds a stream slot.
	assistantScopeRead assistantScope = iota
	// assistantScopeStream covers the expensive agent calls (stream/generate).
	// It applies both the per-user rate limit and the concurrent-stream guard.
	assistantScopeStream
)

const (
	assistantReadWindowMultiplier = 6
	assistantDecrTimeout          = 2 * time.Second
	assistantActiveTTL            = 10 * time.Minute
)

// assistantLimiter enforces a per-user (JWT sub) sliding-minute rate limit and,
// for streams, a concurrent-request guard backed by Redis. Redis outages fail
// open so a cache problem cannot take the assistant down entirely.
func assistantLimiter(rdb *goredis.Client, cfg *config.Config, scope assistantScope) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, role, ok := currentUser(c)
		if !ok {
			abortError(c, http.StatusUnauthorized, "unauthorized", "Autentikasi diperlukan")
			return
		}
		ctx := c.Request.Context()

		if limit := assistantLimitForScope(cfg, scope, role); limit > 0 && rdb != nil {
			key := fmt.Sprintf("ff:assistant:rl:%d:%s:%d", scope, userID, time.Now().Unix()/60)
			count, err := rdb.Incr(ctx, key).Result()
			if err == nil {
				rdb.Expire(ctx, key, 2*time.Minute)
				if count > int64(limit) {
					c.Header("Retry-After", strconv.Itoa(retryAfterFromTTL(ctx, rdb, key)))
					abortError(c, http.StatusTooManyRequests, "rate_limited",
						"Terlalu banyak permintaan assistant, coba lagi nanti")
					return
				}
			}
		}

		if scope == assistantScopeStream && cfg.AssistantMaxConcurrent > 0 && rdb != nil {
			key := fmt.Sprintf("ff:assistant:active:%s", userID)
			active, err := rdb.Incr(ctx, key).Result()
			if err == nil {
				rdb.Expire(ctx, key, assistantActiveTTL)
				if active > int64(cfg.AssistantMaxConcurrent) {
					releaseAssistantSlot(rdb, key)
					abortError(c, http.StatusTooManyRequests, "too_many_streams",
						"Terlalu banyak stream assistant aktif")
					return
				}
				// Release on a detached context: the request context is already
				// cancelled when the client disconnects, which would leave the
				// counter stuck until its TTL expires.
				defer releaseAssistantSlot(rdb, key)
			}
		}

		c.Next()
	}
}

// releaseAssistantSlot decrements a concurrency counter with a bounded context
// that survives request cancellation.
func releaseAssistantSlot(rdb *goredis.Client, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), assistantDecrTimeout)
	defer cancel()
	_ = rdb.Decr(ctx, key).Err()
}

func assistantLimitForScope(cfg *config.Config, scope assistantScope, role domain.UserRole) int {
	base := cfg.AssistantRateLimitPerMin
	if base <= 0 {
		return 0
	}
	if scope == assistantScopeRead {
		return base * assistantReadWindowMultiplier
	}
	return assistantLimitForRole(role, base)
}

// assistantLimitForRole gives operators a larger budget than viewers.
func assistantLimitForRole(role domain.UserRole, base int) int {
	switch role {
	case domain.RoleAdmin:
		return base * 3
	case domain.RolePetugas:
		return base * 2
	default:
		return base
	}
}

func retryAfterFromTTL(ctx context.Context, rdb *goredis.Client, key string) int {
	ttl, err := rdb.TTL(ctx, key).Result()
	if err == nil {
		if seconds := int(ttl.Seconds()); seconds > 0 {
			return seconds
		}
	}
	return 60
}
