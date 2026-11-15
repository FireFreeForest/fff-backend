//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/platform/postgres"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
	"firefreeforest/backend/internal/transport/ws"
)

func TestAPIFlowIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN is not set")
	}

	mr := miniredis.RunT(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("DB_DSN", dsn)
	t.Setenv("JWT_SECRET", "integration-test-secret-min-24-chars")
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("SEED_ADMIN_EMAIL", "admin@fff.local")
	t.Setenv("SEED_ADMIN_PASSWORD", "admin12345")
	t.Setenv("SEED_DEVICE_ID", "")

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := postgres.Open(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.Exec(`TRUNCATE audit_logs, outbox, commands, events, telemetry_fire,
		telemetry_environment, devices, refresh_tokens, push_tokens, users RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.New(db)
	auth := service.NewAuthService(repo, cfg, log)
	if err := auth.SeedAdmin(context.Background()); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	commands := service.NewCommandService(repo, rdb, log, cfg.AutoPumpMax, cfg.MQTTTopicPrefix)
	risk := service.NewRiskEngine(repo, rdb, commands, cfg, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := ws.NewHub(log)
	go hub.Run(ctx)

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(NewRouter(Deps{
		Config:   cfg,
		Repo:     repo,
		Auth:     auth,
		Commands: commands,
		Hub:      hub,
		Redis:    rdb,
		Logger:   log,
	}))
	defer srv.Close()

	var login struct {
		Data struct {
			Tokens struct {
				AccessToken string `json:"access_token"`
			} `json:"tokens"`
		} `json:"data"`
	}
	status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/login", "", map[string]any{
		"email": "admin@fff.local", "password": "admin12345",
	}, &login)
	if status != http.StatusOK || login.Data.Tokens.AccessToken == "" {
		t.Fatalf("login status=%d token empty=%v", status, login.Data.Tokens.AccessToken == "")
	}
	token := login.Data.Tokens.AccessToken

	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/users", token, map[string]any{
		"email": "petugas@fff.local", "password": "petugas12345",
		"name": "Petugas Integration", "role": "petugas",
	}, nil)
	if status != http.StatusCreated {
		t.Fatalf("create petugas status=%d", status)
	}

	var petugasLogin struct {
		Data struct {
			Tokens struct {
				AccessToken string `json:"access_token"`
			} `json:"tokens"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/login", "", map[string]any{
		"email": "petugas@fff.local", "password": "petugas12345",
	}, &petugasLogin)
	if status != http.StatusOK || petugasLogin.Data.Tokens.AccessToken == "" {
		t.Fatalf("petugas login status=%d token empty=%v", status, petugasLogin.Data.Tokens.AccessToken == "")
	}
	petugasToken := petugasLogin.Data.Tokens.AccessToken

	var created struct {
		Data struct {
			DeviceID string `json:"device_id"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/devices", token, map[string]any{
		"device_id": "TEST-001", "name": "Integration Device",
	}, &created)
	if status != http.StatusCreated || created.Data.DeviceID != "TEST-001" {
		t.Fatalf("create device status=%d body=%+v", status, created)
	}

	var deviceStatus struct {
		Data struct {
			RiskLevel string `json:"risk_level"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/status", token, nil, &deviceStatus)
	if status != http.StatusOK || deviceStatus.Data.RiskLevel != "UNKNOWN" {
		t.Fatalf("device status=%d risk=%q", status, deviceStatus.Data.RiskLevel)
	}

	var issued struct {
		Data struct {
			CommandID string `json:"command_id"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/devices/TEST-001/commands", token, map[string]any{
		"action": "SET_PUMP", "payload": map[string]any{"state": true, "duration": 30},
	}, &issued)
	if status != http.StatusAccepted || issued.Data.CommandID == "" {
		t.Fatalf("issue command status=%d command=%q", status, issued.Data.CommandID)
	}

	device, err := repo.GetDeviceByDeviceID(context.Background(), "TEST-001")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	now := time.Now().UTC()
	tel := &domain.TelemetryFire{
		DeviceID: device.ID, FlameDetected: true, SmokeLevel: 120,
		RecordedAt: now, ReceivedAt: now,
	}
	if err := repo.CreateFire(context.Background(), tel); err != nil {
		t.Fatalf("create fire telemetry: %v", err)
	}
	evaluation, err := risk.RecordFire(context.Background(), device, tel)
	if err != nil {
		t.Fatalf("record fire: %v", err)
	}
	if evaluation.RiskLevel != service.RiskDarurat || len(evaluation.Events) != 1 {
		t.Fatalf("evaluation risk=%s events=%d", evaluation.RiskLevel, len(evaluation.Events))
	}

	ackedCmd, changed, err := repo.AckCommand(context.Background(), device.ID, issued.Data.CommandID, domain.CommandSuccess, "Pump activated")
	if err != nil || !changed || ackedCmd.Status != domain.CommandSuccess {
		t.Fatalf("ack command changed=%v status=%v err=%v", changed, ackedCmd, err)
	}

	outboxRows, err := repo.ClaimOutbox(context.Background(), 10)
	if err != nil {
		t.Fatalf("claim outbox: %v", err)
	}
	if len(outboxRows) < 2 {
		t.Fatalf("outbox rows = %d, want at least 2", len(outboxRows))
	}
	for _, row := range outboxRows {
		if row.QoS != 1 {
			t.Fatalf("outbox qos = %d, want 1", row.QoS)
		}
	}
	if err := repo.MarkOutboxSent(context.Background(), outboxRows[0].ID); err != nil {
		t.Fatalf("mark outbox sent: %v", err)
	}

	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/push/tokens", token, map[string]any{
		"token": "integration-test-token", "platform": "android",
	}, nil)
	if status != http.StatusNoContent {
		t.Fatalf("register push token status=%d", status)
	}
	status = doJSON(t, http.MethodDelete, srv.URL+"/api/v1/push/tokens/integration-test-token", token, nil, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete push token status=%d", status)
	}

	var events struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events?status=active", token, nil, &events)
	if status != http.StatusOK || events.Meta.Total < 1 || len(events.Data) == 0 {
		t.Fatalf("list active events status=%d total=%d", status, events.Meta.Total)
	}
	eventID := events.Data[0].ID

	var acked struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/events/"+eventID+"/acknowledge", token, nil, &acked)
	if status != http.StatusOK || acked.Data.Status != string(domain.EventAcknowledged) {
		t.Fatalf("ack status=%d event_status=%q", status, acked.Data.Status)
	}

	var resolved struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/events/"+eventID+"/resolve", token, nil, nil)
	if status != http.StatusForbidden {
		t.Fatalf("admin resolve status=%d, want %d Forbidden", status, http.StatusForbidden)
	}

	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/events/"+eventID+"/resolve", petugasToken, nil, &resolved)
	if status != http.StatusOK || resolved.Data.Status != string(domain.EventResolved) {
		t.Fatalf("petugas resolve status=%d event_status=%q", status, resolved.Data.Status)
	}

	var dashboard struct {
		Data struct {
			Devices struct {
				Total int `json:"total"`
			} `json:"devices"`
			ActiveEvents struct {
				Total int64 `json:"total"`
			} `json:"active_events"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/dashboard/summary", token, nil, &dashboard)
	if status != http.StatusOK || dashboard.Data.Devices.Total != 1 || dashboard.Data.ActiveEvents.Total != 0 {
		t.Fatalf("dashboard status=%d devices=%d active=%d", status, dashboard.Data.Devices.Total, dashboard.Data.ActiveEvents.Total)
	}

	var ticket struct {
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/realtime/ticket", token, nil, &ticket)
	if status != http.StatusOK || ticket.Data.Ticket == "" {
		t.Fatalf("ws ticket status=%d", status)
	}
	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/api/v1/ws?ticket=" + ticket.Data.Ticket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var welcome struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&welcome); err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if welcome.Type != "connected" {
		t.Fatalf("ws welcome type=%q", welcome.Type)
	}
}

func doJSON(t *testing.T, method, url, token string, body, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s status=%d: %v body=%s", method, url, resp.StatusCode, err, raw)
		}
	}
	if resp.StatusCode >= 500 {
		t.Logf("server error body: %s", raw)
	}
	return resp.StatusCode
}
