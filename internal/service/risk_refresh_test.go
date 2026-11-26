package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
)

func newTestRiskEngine(t *testing.T, timeout time.Duration) (*RiskEngine, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &RiskEngine{
		rdb: rdb,
		cfg: &config.Config{TelemetryRefreshTimeout: timeout},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, rdb
}

func TestWaitForTelemetryAck(t *testing.T) {
	ctx := context.Background()
	e, rdb := newTestRiskEngine(t, 150*time.Millisecond)

	if err := rdb.Set(ctx, telemetryAckKeyPrefix+"cmd-success", string(domain.CommandSuccess), time.Minute).Err(); err != nil {
		t.Fatalf("seed success ack: %v", err)
	}
	if !e.waitForTelemetryAck(ctx, "cmd-success") {
		t.Fatal("expected success ack to return true")
	}

	if err := rdb.Set(ctx, telemetryAckKeyPrefix+"cmd-failed", string(domain.CommandFailed), time.Minute).Err(); err != nil {
		t.Fatalf("seed failed ack: %v", err)
	}
	if e.waitForTelemetryAck(ctx, "cmd-failed") {
		t.Fatal("expected failed ack to return false")
	}

	start := time.Now()
	if e.waitForTelemetryAck(ctx, "cmd-missing") {
		t.Fatal("expected missing ack to time out as false")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("expected wait before timing out, got %v", elapsed)
	}
}

func TestWaitForTelemetryAckContextCanceled(t *testing.T) {
	e, _ := newTestRiskEngine(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e.waitForTelemetryAck(ctx, "cmd-canceled") {
		t.Fatal("expected canceled context to return false")
	}
}

func TestLatestCacheReceivedAt(t *testing.T) {
	ctx := context.Background()
	_, rdb := newTestRiskEngine(t, time.Minute)

	if got := latestCacheReceivedAt(ctx, rdb, "DEV-1"); !got.IsZero() {
		t.Fatalf("expected zero time for empty cache, got %v", got)
	}

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	envRaw, _ := json.Marshal(domain.TelemetryEnvironment{ReceivedAt: old})
	if err := rdb.HSet(ctx, "ff:latest:DEV-1", "environment", envRaw).Err(); err != nil {
		t.Fatalf("hset environment: %v", err)
	}
	if got := latestCacheReceivedAt(ctx, rdb, "DEV-1"); !got.Equal(old) {
		t.Fatalf("received_at = %v, want %v", got, old)
	}

	recent := old.Add(time.Hour)
	fireRaw, _ := json.Marshal(domain.TelemetryFire{ReceivedAt: recent})
	if err := rdb.HSet(ctx, "ff:latest:DEV-1", "fire", fireRaw).Err(); err != nil {
		t.Fatalf("hset fire: %v", err)
	}
	if got := latestCacheReceivedAt(ctx, rdb, "DEV-1"); !got.Equal(recent) {
		t.Fatalf("received_at = %v, want newest %v", got, recent)
	}

	devRaw, _ := json.Marshal(domain.TelemetryDevice{ReceivedAt: old.Add(-time.Hour)})
	if err := rdb.HSet(ctx, "ff:latest:DEV-1", "device", devRaw).Err(); err != nil {
		t.Fatalf("hset device: %v", err)
	}
	if got := latestCacheReceivedAt(ctx, rdb, "DEV-1"); !got.Equal(recent) {
		t.Fatalf("received_at = %v, want newest %v", got, recent)
	}
}
