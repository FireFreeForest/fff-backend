package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/service"
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

func TestClassifyAuthError(t *testing.T) {
	cases := []struct {
		err        error
		wantReason string
		wantStatus int
	}{
		{service.ErrMCPKeyInvalid, "invalid_token", http.StatusUnauthorized},
		{service.ErrMCPKeyInactive, "inactive_key", http.StatusUnauthorized},
		{service.ErrMCPKeyExpired, "expired_key", http.StatusUnauthorized},
		{errors.New("database down"), "backend_error", http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		reason, status, message := classifyAuthError(tc.err)
		if reason != tc.wantReason || status != tc.wantStatus || message == "" {
			t.Fatalf("classifyAuthError(%v) = %q, %d, %q", tc.err, reason, status, message)
		}
	}
}

func TestAllowMCPRequest(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	keyID := uuid.NewString()

	for i := 0; i < 2; i++ {
		allowed, retryAfter := allowMCPRequest(ctx, rdb, keyID, 2)
		if !allowed || retryAfter != 0 {
			t.Fatalf("request %d = %t, %d; want allowed", i+1, allowed, retryAfter)
		}
	}
	allowed, retryAfter := allowMCPRequest(ctx, rdb, keyID, 2)
	if allowed {
		t.Fatal("third request should be rate limited")
	}
	if retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("retryAfter = %d, want 1..60", retryAfter)
	}

	// A different key has its own window.
	if allowed, _ := allowMCPRequest(ctx, rdb, uuid.NewString(), 2); !allowed {
		t.Fatal("other key should not be rate limited")
	}
}

func TestAllowMCPRequestRedisFailureFailsOpen(t *testing.T) {
	rdb := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:0", DialTimeout: 0})
	defer rdb.Close()
	if allowed, _ := allowMCPRequest(context.Background(), rdb, uuid.NewString(), 1); !allowed {
		t.Fatal("redis outage should not block MCP requests")
	}
}

func TestPrincipalContext(t *testing.T) {
	if principal := PrincipalFromContext(context.Background()); principal != nil {
		t.Fatalf("principal = %+v, want nil", principal)
	}
	keyID := uuid.New()
	principal := &service.MCPPrincipal{
		KeyID:   keyID,
		KeyName: "test",
		Role:    domain.RolePetugas,
	}
	ctx := WithPrincipal(context.Background(), principal)
	got := PrincipalFromContext(ctx)
	if got == nil || got.KeyID != keyID || got.Role != domain.RolePetugas {
		t.Fatalf("principal = %+v", got)
	}
}

func TestAuthMiddlewareMissingToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := AuthMiddleware(nil, nil, 0, log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if w.Header().Get("Retry-After") != "5" {
		t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
	}

	keys := service.NewMCPKeyService(nil)
	handler = AuthMiddleware(keys, nil, 0, log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	}))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if w.Header().Get("WWW-Authenticate") != `Bearer realm="mcp"` {
		t.Fatalf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
	}
}
