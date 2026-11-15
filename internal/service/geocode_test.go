package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestGeocoderReverseCachesResult(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		if r.URL.Query().Get("lat") == "" || r.URL.Query().Get("lon") == "" {
			t.Error("missing coordinates")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"display_name": "Jalan Merdeka, Palangka Raya, Kalimantan Tengah, Indonesia",
			"address": {
				"road": "Jalan Merdeka",
				"village": "Palangka",
				"city": "Palangka Raya",
				"state": "Kalimantan Tengah"
			}
		}`)
	}))
	defer server.Close()

	geocoder := NewGeocoder(rdb, server.URL, "FireFreeForest/test", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result, err := geocoder.Reverse(context.Background(), -2.2, 113.9)
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if result.Label != "Jalan Merdeka, Palangka, Palangka Raya, Kalimantan Tengah" {
		t.Fatalf("label = %q", result.Label)
	}
	if result.Provider != geocodeProvider {
		t.Fatalf("provider = %q", result.Provider)
	}

	if _, err := geocoder.Reverse(context.Background(), -2.2, 113.9); err != nil {
		t.Fatalf("reverse cached: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}
