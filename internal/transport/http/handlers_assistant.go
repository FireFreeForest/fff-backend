package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
)

const (
	assistantProxyPrefix = "/api/v1/mastra"
	assistantBodyLimit   = 1 << 20 // 1 MiB
)

const mastraAgentID = "fff-data-analyst"

type mastraProxy struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
	log    *slog.Logger
}

func newMastraProxy(rawURL string, log *slog.Logger) (*mastraProxy, error) {
	if log == nil {
		log = slog.Default()
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, errors.New("MASTRA_URL tidak valid")
	}

	p := &mastraProxy{target: target, log: log}
	p.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			path := strings.TrimPrefix(r.In.URL.Path, assistantProxyPrefix)
			if path == "" {
				path = "/"
			}
			r.Out.URL.Path = path
			r.Out.URL.RawPath = ""
			r.SetURL(target)
			r.Out.Host = target.Host

			// The backend terminates the client connection, so any
			// X-Forwarded-* value from the client is untrusted. Drop it and let
			// the proxy rebuild the chain from the real connection.
			for _, header := range []string{
				"X-Forwarded-For",
				"X-Forwarded-Host",
				"X-Forwarded-Proto",
				"X-Real-IP",
			} {
				r.Out.Header.Del(header)
			}
			r.SetXForwarded()

			// The backend only validates the JWT; Mastra verifies the same token
			// with the shared secret and derives the memory owner from its sub.
			if auth := r.In.Header.Get("Authorization"); auth != "" {
				r.Out.Header.Set("Authorization", auth)
			} else {
				r.Out.Header.Del("Authorization")
			}
			r.Out.Header.Del("Cookie")
			// The memory owner is derived from the JWT sub via
			// mapUserToResourceId; never let a client override it.
			r.Out.Header.Del("X-Resource-Id")
			// Blocks the Mastra dev playground auth bypass header.
			r.Out.Header.Del("X-Mastra-Dev-Playground")
		},
		ModifyResponse: func(res *http.Response) error {
			// CORS sepenuhnya dikelola backend.
			// Buang header CORS dari Mastra agar tidak dobel.
			for key := range res.Header {
				if strings.HasPrefix(strings.ToLower(key), "access-control-") {
					res.Header.Del(key)
				}
			}
			return nil
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("mastra proxy error", "error", err, "path", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{
					"code":    "assistant_unavailable",
					"message": "Layanan assistant tidak tersedia",
				},
			})
		},
	}
	return p, nil
}

func (p *mastraProxy) handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		upstream := upstreamAssistantPath(c.Request.URL.Path)
		if !allowedAssistantRequest(c.Request.Method, upstream) {
			abortError(c, http.StatusForbidden, "forbidden", "Path assistant tidak diizinkan")
			return
		}
		if c.Request.Body != nil && methodHasBody(c.Request.Method) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, assistantBodyLimit)
		}

		// Streams can outlive the server-wide write timeout; clear the deadline
		// for this connection when the writer supports it. Only agent calls need
		// it: leaving the deadline in place for ordinary requests keeps the
		// server's slow-writer protection.
		if isAssistantStreamRequest(c.Request.Method, upstream) {
			_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
		}

		userID, role, _ := currentUser(c)
		start := time.Now()
		p.proxy.ServeHTTP(c.Writer, c.Request)
		p.log.Info("assistant proxy",
			"request_id", requestID(c),
			"user_id", userID,
			"role", role,
			"method", c.Request.Method,
			"upstream_path", upstream,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

// assistantGuard picks the limiter scope from the request path: expensive agent
// calls get the strict stream budget plus the concurrency guard, everything
// else gets the looser read budget.
func assistantGuard(rdb *goredis.Client, cfg *config.Config) gin.HandlerFunc {
	read := assistantLimiter(rdb, cfg, assistantScopeRead)
	stream := assistantLimiter(rdb, cfg, assistantScopeStream)
	return func(c *gin.Context) {
		if isAssistantStreamRequest(c.Request.Method, upstreamAssistantPath(c.Request.URL.Path)) {
			stream(c)
			return
		}
		read(c)
	}
}

func requestID(c *gin.Context) string {
	raw, _ := c.Get("request_id")
	id, _ := raw.(string)
	return id
}

func upstreamAssistantPath(fullPath string) string {
	path := strings.TrimPrefix(fullPath, assistantProxyPrefix)
	if path == "" {
		return "/"
	}
	return path
}

func methodHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func isAssistantStreamRequest(method, upstreamPath string) bool {
	if method != http.MethodPost {
		return false
	}
	return upstreamPath == "/api/agents/"+mastraAgentID+"/stream" ||
		upstreamPath == "/api/agents/"+mastraAgentID+"/generate"
}

// allowedAssistantRequest is a strict method+path allowlist. Only the read-only
// agent endpoints and the memory thread routes the chat UI needs are exposed;
// thread clone/transfer, stored data, observability and Studio routes stay
// unreachable even though they exist upstream.
func allowedAssistantRequest(method, upstreamPath string) bool {
	// Normalize before matching: dot segments must never reach the allowlist as
	// opaque thread IDs, because the upstream may normalize them differently.
	p := strings.TrimSuffix(upstreamPath, "/")
	if p == "" {
		p = "/"
	}
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		return false
	}

	switch p {
	case "/api/agents":
		return method == http.MethodGet
	case "/api/agents/" + mastraAgentID:
		return method == http.MethodGet
	case "/api/agents/" + mastraAgentID + "/stream",
		"/api/agents/" + mastraAgentID + "/generate":
		return method == http.MethodPost
	case "/api/memory/threads":
		return method == http.MethodGet || method == http.MethodPost
	}

	rest, ok := strings.CutPrefix(p, "/api/memory/threads/")
	if !ok || rest == "" {
		return false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		if !validThreadID(parts[0]) {
			return false
		}
		return method == http.MethodGet || method == http.MethodPatch || method == http.MethodDelete
	case 2:
		return validThreadID(parts[0]) && parts[1] == "messages" && method == http.MethodGet
	default:
		return false
	}
}

// validThreadID keeps thread identifiers to an opaque, URL-safe charset. It
// rejects empty IDs, dot segments, path separators and percent-encoded bytes so
// the upstream cannot be reached through a different route after normalization.
func validThreadID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-',
			r == '_':
		default:
			return false
		}
	}
	return true
}
