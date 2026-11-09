package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDocsRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerDocsRoutes(r)

	t.Run("index", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("content-type = %q, want text/html", ct)
		}
		if !strings.Contains(w.Body.String(), "/docs/openapi.yaml") {
			t.Fatal("index tidak mereferensikan /docs/openapi.yaml")
		}
	})

	t.Run("spec", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
			t.Fatalf("content-type = %q, want application/yaml", ct)
		}
		body := w.Body.String()
		if !strings.Contains(body, "openapi: 3.0.3") {
			t.Fatal("spec tidak berisi versi OpenAPI")
		}
		if !strings.Contains(body, "/users") {
			t.Fatal("spec tidak memuat endpoint /users")
		}
	})
}
