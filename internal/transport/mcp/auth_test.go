package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		token string
		ok    bool
	}{
		{"valid", "Bearer mcp_0123456789ab_secret", "mcp_0123456789ab_secret", true},
		{"case insensitive", "bearer token", "token", true},
		{"extra spaces", "  Bearer   token  ", "token", true},
		{"empty", "", "", false},
		{"missing scheme", "token", "", false},
		{"wrong scheme", "Basic token", "", false},
		{"empty token", "Bearer   ", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, ok := bearerToken(tc.raw)
			if token != tc.token || ok != tc.ok {
				t.Fatalf("bearerToken(%q) = %q, %t; want %q, %t", tc.raw, token, ok, tc.token, tc.ok)
			}
		})
	}
}

func TestSecureCompare(t *testing.T) {
	if !secureCompare("secret-token", "secret-token") {
		t.Fatal("identical tokens should match")
	}
	if secureCompare("secret-token", "other-token") {
		t.Fatal("different tokens must not match")
	}
	if secureCompare("secret-token", "") {
		t.Fatal("empty token must not match")
	}
}

func TestAllowMCPRequest(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		allowed, retryAfter := allowMCPRequest(ctx, rdb, "env", 2)
		if !allowed || retryAfter != 0 {
			t.Fatalf("request %d = %t, %d; want allowed", i+1, allowed, retryAfter)
		}
	}
	allowed, retryAfter := allowMCPRequest(ctx, rdb, "env", 2)
	if allowed {
		t.Fatal("third request should be rate limited")
	}
	if retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("retryAfter = %d, want 1..60", retryAfter)
	}

	// A different scope has its own window.
	if allowed, _ := allowMCPRequest(ctx, rdb, uuid.NewString(), 2); !allowed {
		t.Fatal("other scope should not be rate limited")
	}
}

func TestAllowMCPRequestRedisFailureFailsOpen(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0", DialTimeout: 0})
	defer rdb.Close()
	if allowed, _ := allowMCPRequest(context.Background(), rdb, "env", 1); !allowed {
		t.Fatal("redis outage should not block MCP requests")
	}
}

func TestAuthMiddleware(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("unconfigured", func(t *testing.T) {
		handler := AuthMiddleware("", nil, 0, log)(next)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
		if w.Header().Get("Retry-After") != "5" {
			t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
		}
	})

	t.Run("missing token", func(t *testing.T) {
		handler := AuthMiddleware("secret", nil, 0, log)(next)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if w.Header().Get("WWW-Authenticate") != `Bearer realm="mcp"` {
			t.Fatalf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		handler := AuthMiddleware("secret", nil, 0, log)(next)
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer nope")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		handler := AuthMiddleware("secret", nil, 0, log)(next)
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}
