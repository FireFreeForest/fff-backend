package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/service"
)

func testConfig() *config.Config {
	return &config.Config{
		JWTSecret:                strings.Repeat("x", 32),
		AssistantRateLimitPerMin: 3,
		AssistantMaxConcurrent:   1,
	}
}

func TestAllowedAssistantRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"list agents", http.MethodGet, "/api/agents", true},
		{"get agent", http.MethodGet, "/api/agents/fff-data-analyst", true},
		{"stream", http.MethodPost, "/api/agents/fff-data-analyst/stream", true},
		{"generate", http.MethodPost, "/api/agents/fff-data-analyst/generate", true},
		{"list threads", http.MethodGet, "/api/memory/threads", true},
		{"create thread", http.MethodPost, "/api/memory/threads", true},
		{"get thread", http.MethodGet, "/api/memory/threads/t1", true},
		{"rename thread", http.MethodPatch, "/api/memory/threads/t1", true},
		{"delete thread", http.MethodDelete, "/api/memory/threads/t1", true},
		{"list messages", http.MethodGet, "/api/memory/threads/t1/messages", true},
		{"thread id safe charset", http.MethodGet, "/api/memory/threads/thread_01-ab", true},
		{"trailing slash trimmed", http.MethodGet, "/api/memory/threads/", true},
		{"dot segment", http.MethodGet, "/api/memory/threads/..", false},
		{"parent traversal", http.MethodGet, "/api/memory/threads/../../observability/traces", false},
		{"encoded traversal", http.MethodGet, "/api/memory/threads/%2e%2e", false},
		{"double encoded traversal", http.MethodGet, "/api/memory/threads/%252e%252e", false},
		{"thread id with separator", http.MethodGet, "/api/memory/threads/t1%2fx", false},
		{"thread id too long", http.MethodGet, "/api/memory/threads/" + strings.Repeat("a", 129), false},
		{"thread clone", http.MethodPost, "/api/memory/threads/t1/clone", false},
		{"thread transfer", http.MethodPost, "/api/memory/threads/t1/transfer", false},
		{"working memory", http.MethodGet, "/api/memory/threads/t1/working-memory", false},
		{"send-message not allowed", http.MethodPost, "/api/agents/fff-data-analyst/send-message", false},
		{"stored data", http.MethodGet, "/api/stored/foo", false},
		{"observability", http.MethodGet, "/api/observability/traces", false},
		{"other agent", http.MethodGet, "/api/agents/other", false},
		{"agents via POST", http.MethodPost, "/api/agents", false},
		{"stream via GET", http.MethodGet, "/api/agents/fff-data-analyst/stream", false},
		{"empty path", http.MethodGet, "/", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowedAssistantRequest(tc.method, tc.path); got != tc.want {
				t.Fatalf("allowedAssistantRequest(%s, %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func authContext(t *testing.T, r *gin.Engine, userID uuid.UUID, role domain.UserRole) {
	t.Helper()
	r.Use(func(c *gin.Context) {
		setAuth(c, userID, role)
		c.Next()
	})
}

func TestAssistantLimiterRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	cfg := testConfig()
	user := uuid.New()

	r := gin.New()
	authContext(t, r, user, domain.RoleViewer)
	r.GET("/x", assistantLimiter(rdb, cfg, assistantScopeRead), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for i := 1; i <= cfg.AssistantRateLimitPerMin*assistantReadWindowMultiplier; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, w.Code)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}

	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "rate_limited" {
		t.Fatalf("error code = %q", payload.Error.Code)
	}
}

func TestAssistantLimiterSeparateUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	cfg := testConfig()
	cfg.AssistantRateLimitPerMin = 1
	limit := cfg.AssistantRateLimitPerMin * assistantReadWindowMultiplier

	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		setAuth(c, uuid.MustParse(c.Query("user")), domain.RoleViewer)
	}, assistantLimiter(rdb, cfg, assistantScopeRead), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	first := uuid.New().String()
	second := uuid.New().String()

	for i := 0; i < limit; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?user="+first, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("user1 request %d: %d", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?user="+first, nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("user1 over limit = %d, want 429", w.Code)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?user="+second, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("user2 should have its own bucket, got %d", w.Code)
	}
}

func TestAssistantLimiterConcurrentStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	cfg := testConfig()
	cfg.AssistantRateLimitPerMin = 0 // isolate the concurrency guard
	cfg.AssistantMaxConcurrent = 1
	user := uuid.New()

	release := make(chan struct{})
	entered := make(chan struct{})
	r := gin.New()
	authContext(t, r, user, domain.RoleViewer)
	r.POST("/stream", assistantLimiter(rdb, cfg, assistantScopeStream), func(c *gin.Context) {
		close(entered)
		<-release
		c.Status(http.StatusOK)
	})

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/stream", nil))
		done <- w.Code
	}()

	<-entered

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/stream", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second concurrent stream = %d, want 429", w.Code)
	}

	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first stream status = %d", code)
	}

	key := "ff:assistant:active:" + user.String()
	if got, _ := mr.Get(key); got != "0" {
		t.Fatalf("active counter after streams = %q, want 0", got)
	}
}

func TestAssistantConcurrentSlotReleasedOnCancelledContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	cfg := testConfig()
	cfg.AssistantRateLimitPerMin = 0
	cfg.AssistantMaxConcurrent = 1
	user := uuid.New()

	ctx, cancel := context.WithCancel(context.Background())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuth(c, user, domain.RoleViewer)
		c.Next()
	})
	r.POST("/stream", assistantLimiter(rdb, cfg, assistantScopeStream), func(c *gin.Context) {
		cancel() // simulate client disconnect mid-stream
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	key := "ff:assistant:active:" + user.String()
	if got, _ := mr.Get(key); got != "0" {
		t.Fatalf("active counter after cancelled stream = %q, want 0 (slot leaked)", got)
	}
}

func newProxyTestServer(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	proxy, err := newMastraProxy(upstreamURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	authContext(t, r, uuid.New(), domain.RoleViewer)
	r.Any(assistantProxyPrefix+"/*path", proxy.handler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestMastraProxyForwardsAndStrips(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Errorf("upstream Authorization = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("upstream Cookie should be stripped, got %q", got)
		}
		if r.URL.Path != "/api/agents" {
			t.Errorf("upstream path = %q, want /api/agents", r.URL.Path)
		}
		// Client-supplied forwarding headers must never reach the upstream.
		if got := r.Header.Get("X-Forwarded-For"); strings.Contains(got, "1.2.3.4") || got == "" {
			t.Errorf("upstream X-Forwarded-For = %q, want only the real client IP", got)
		}
		if got := r.Header.Get("X-Forwarded-Host"); got == "evil.example" {
			t.Errorf("client X-Forwarded-Host leaked upstream")
		}
		if got := r.Header.Get("X-Forwarded-Proto"); got != "http" {
			t.Errorf("upstream X-Forwarded-Proto = %q, want http", got)
		}
		if got := r.Header.Get("X-Real-IP"); got != "" {
			t.Errorf("upstream X-Real-IP = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	srv := newProxyTestServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+"/api/agents", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
}

func newProxyCORSTestServer(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	proxy, err := newMastraProxy(upstreamURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"http://localhost:5173"}))
	authContext(t, r, uuid.New(), domain.RoleViewer)
	r.Any(assistantProxyPrefix+"/*path", proxy.handler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestMastraProxyStripsUpstreamCORSHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "X-Foo")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	srv := newProxyCORSTestServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+"/api/agents", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	origins := resp.Header.Values("Access-Control-Allow-Origin")
	if len(origins) != 1 {
		t.Fatalf("Access-Control-Allow-Origin values = %v, want exactly 1", origins)
	}
	if origins[0] != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", origins[0])
	}
	if got := resp.Header.Values("Access-Control-Expose-Headers"); len(got) != 0 {
		t.Fatalf("Access-Control-Expose-Headers should be stripped, got %v", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials should be stripped, got %q", got)
	}
}

func TestMastraProxyPreflightCORS(t *testing.T) {
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer upstream.Close()

	srv := newProxyCORSTestServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodOptions, srv.URL+assistantProxyPrefix+"/api/agents", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	origins := resp.Header.Values("Access-Control-Allow-Origin")
	if len(origins) != 1 || origins[0] != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin values = %v", origins)
	}
	if called {
		t.Fatal("upstream must not be called for preflight")
	}
}

func TestMastraProxyRejectsDisallowedPath(t *testing.T) {
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer upstream.Close()

	srv := newProxyTestServer(t, upstream.URL)

	resp, err := http.Post(srv.URL+assistantProxyPrefix+"/api/memory/threads/t1/transfer", "application/json", nil)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if called {
		t.Fatal("upstream must not be called for a disallowed path")
	}
}

func TestMastraProxyRejectsTraversalPath(t *testing.T) {
	var called bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer upstream.Close()

	srv := newProxyTestServer(t, upstream.URL)

	for _, path := range []string{
		"/api/memory/threads/..",
		"/api/memory/threads/../../observability/traces",
		"/api/memory/threads/%252e%252e",
	} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
	if called {
		t.Fatal("upstream must not be called for a traversal path")
	}
}

func TestMastraProxyFlushesStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, "data: two\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	srv := newProxyTestServer(t, upstream.URL)

	resp, err := http.Post(srv.URL+assistantProxyPrefix+"/api/agents/fff-data-analyst/stream", "application/json", nil)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "data: one") || !strings.Contains(string(body), "data: two") {
		t.Fatalf("stream body = %q", body)
	}
}

func TestAssistantRouteRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := testConfig()
	auth := service.NewAuthService(nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	r := gin.New()
	r.GET("/protected", authMiddleware(auth), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", w.Code)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, service.Claims{
		Role: string(domain.RoleViewer),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Issuer:    "fire-free-forest",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	})
	raw, err := token.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", w.Code)
	}
}

func signedAccessToken(t *testing.T, cfg *config.Config, role domain.UserRole) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, service.Claims{
		Role: string(role),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Issuer:    "fire-free-forest",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	})
	raw, err := token.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func newTestRouter(t *testing.T, cfg *config.Config, rdb *goredis.Client) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(Deps{
		Config: cfg,
		Auth:   service.NewAuthService(nil, cfg, log),
		Redis:  rdb,
		Logger: log,
	})
}

func TestAssistantRouteAbsentWhenURLUnset(t *testing.T) {
	cfg := &config.Config{JWTSecret: strings.Repeat("x", 32)}
	srv := httptest.NewServer(newTestRouter(t, cfg, nil))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+"/api/agents", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+signedAccessToken(t, cfg, domain.RoleViewer))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when MASTRA_URL is empty", resp.StatusCode)
	}
}

func TestAssistantRouteWithJWT(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("upstream missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agents":[]}`)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		JWTSecret:                strings.Repeat("x", 32),
		MastraURL:                upstream.URL,
		AssistantRateLimitPerMin: 20,
		AssistantMaxConcurrent:   2,
	}
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	srv := httptest.NewServer(newTestRouter(t, cfg, rdb))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+"/api/agents", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+signedAccessToken(t, cfg, domain.RoleViewer))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	resp, err = http.Get(srv.URL + assistantProxyPrefix + "/api/agents")
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", resp.StatusCode)
	}
}

func TestMastraProxyStripsSpoofedHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Resource-Id"); got != "" {
			t.Errorf("X-Resource-Id must be stripped, got %q", got)
		}
		if got := r.Header.Get("X-Mastra-Dev-Playground"); got != "" {
			t.Errorf("X-Mastra-Dev-Playground must be stripped, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := newProxyTestServer(t, upstream.URL)

	req, err := http.NewRequest(http.MethodGet, srv.URL+assistantProxyPrefix+"/api/memory/threads", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Resource-Id", "another-user")
	req.Header.Set("X-Mastra-Dev-Playground", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
