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
	if err := db.Exec(`TRUNCATE event_timeline, audit_logs, outbox, commands, events, telemetry_fire,
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

	var userSearch struct {
		Data []struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/users?q=PETUGAS", token, nil, &userSearch)
	if status != http.StatusOK || userSearch.Meta.Total != 1 || len(userSearch.Data) != 1 || userSearch.Data[0].Email != "petugas@fff.local" {
		t.Fatalf("user search name status=%d total=%d data=%+v", status, userSearch.Meta.Total, userSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/users?q=petugas%40fff.local", token, nil, &userSearch)
	if status != http.StatusOK || userSearch.Meta.Total != 1 || len(userSearch.Data) != 1 || userSearch.Data[0].Name != "Petugas Integration" {
		t.Fatalf("user search email status=%d total=%d data=%+v", status, userSearch.Meta.Total, userSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/users?q=tidak-ada", token, nil, &userSearch)
	if status != http.StatusOK || userSearch.Meta.Total != 0 || len(userSearch.Data) != 0 {
		t.Fatalf("user search no match status=%d total=%d data=%+v", status, userSearch.Meta.Total, userSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/users?q=+", token, nil, &userSearch)
	if status != http.StatusOK || userSearch.Meta.Total != 2 {
		t.Fatalf("user search empty status=%d total=%d", status, userSearch.Meta.Total)
	}

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

	var generated struct {
		Data struct {
			DeviceID string `json:"device_id"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/devices", token, map[string]any{
		"name": "Generated Integration Device", "location_name": "Generated Location",
	}, &generated)
	if status != http.StatusCreated || !strings.HasPrefix(generated.Data.DeviceID, "FFF-") || len(generated.Data.DeviceID) != 12 {
		t.Fatalf("create generated device status=%d body=%+v", status, generated)
	}

	var deviceSearch struct {
		Data []struct {
			DeviceID     string `json:"device_id"`
			Name         string `json:"name"`
			LocationName string `json:"location_name"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q=generated", token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 1 || len(deviceSearch.Data) != 1 || deviceSearch.Data[0].DeviceID != generated.Data.DeviceID {
		t.Fatalf("device search name status=%d total=%d data=%+v", status, deviceSearch.Meta.Total, deviceSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q=Generated%20Location", token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 1 || len(deviceSearch.Data) != 1 || deviceSearch.Data[0].Name != "Generated Integration Device" {
		t.Fatalf("device search location status=%d total=%d data=%+v", status, deviceSearch.Meta.Total, deviceSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q="+generated.Data.DeviceID, token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 1 || len(deviceSearch.Data) != 1 || deviceSearch.Data[0].DeviceID != generated.Data.DeviceID {
		t.Fatalf("device search device_id status=%d total=%d data=%+v", status, deviceSearch.Meta.Total, deviceSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q=tidak-ada", token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 0 || len(deviceSearch.Data) != 0 {
		t.Fatalf("device search no match status=%d total=%d data=%+v", status, deviceSearch.Meta.Total, deviceSearch.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q=%25", token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 0 {
		t.Fatalf("device search literal percent status=%d total=%d", status, deviceSearch.Meta.Total)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q=_", token, nil, &deviceSearch)
	if status != http.StatusOK || deviceSearch.Meta.Total != 0 {
		t.Fatalf("device search literal underscore status=%d total=%d", status, deviceSearch.Meta.Total)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices?q="+strings.Repeat("a", 101), token, nil, nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("device search too long status=%d", status)
	}

	var deviceStatus struct {
		Data struct {
			RiskLevel     string     `json:"risk_level"`
			Status        string     `json:"status"`
			OnlineSince   *time.Time `json:"online_since"`
			UptimeSeconds int64      `json:"uptime_seconds"`
			Device        struct {
				Status        string     `json:"status"`
				OnlineSince   *time.Time `json:"online_since"`
				UptimeSeconds int64      `json:"uptime_seconds"`
			} `json:"device"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/status", token, nil, &deviceStatus)
	if status != http.StatusOK || deviceStatus.Data.RiskLevel != "UNKNOWN" {
		t.Fatalf("device status=%d risk=%q", status, deviceStatus.Data.RiskLevel)
	}
	if deviceStatus.Data.Status != "offline" || deviceStatus.Data.UptimeSeconds != 0 || deviceStatus.Data.OnlineSince != nil {
		t.Fatalf("initial uptime status=%q uptime=%d online_since=%v", deviceStatus.Data.Status, deviceStatus.Data.UptimeSeconds, deviceStatus.Data.OnlineSince)
	}
	if deviceStatus.Data.Device.Status != "offline" || deviceStatus.Data.Device.UptimeSeconds != 0 || deviceStatus.Data.Device.OnlineSince != nil {
		t.Fatalf("initial nested device uptime status=%q uptime=%d online_since=%v", deviceStatus.Data.Device.Status, deviceStatus.Data.Device.UptimeSeconds, deviceStatus.Data.Device.OnlineSince)
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
	notifier := service.NewNotifier(nil, repo, domain.SeverityError, nil, log)
	notifier.NotifyEvent(context.Background(), device, evaluation.Events[0])
	notifier.NotifyEvent(context.Background(), device, evaluation.Events[0])

	aggregateBase := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	envRows := []*domain.TelemetryEnvironment{
		{DeviceID: device.ID, Temperature: floatPtr(20), Humidity: floatPtr(60), SoilMoisture: 100, RecordedAt: aggregateBase, ReceivedAt: aggregateBase},
		{DeviceID: device.ID, Temperature: floatPtr(30), Humidity: floatPtr(70), SoilMoisture: 200, RecordedAt: aggregateBase.Add(2 * time.Minute), ReceivedAt: aggregateBase},
		{DeviceID: device.ID, Humidity: nil, SoilMoisture: 300, RecordedAt: aggregateBase.Add(5 * time.Minute), ReceivedAt: aggregateBase},
		{DeviceID: device.ID, Temperature: floatPtr(50), Humidity: floatPtr(90), SoilMoisture: 400, RecordedAt: aggregateBase.Add(7 * time.Minute), ReceivedAt: aggregateBase},
	}
	for _, row := range envRows {
		if err := repo.CreateEnvironment(context.Background(), row); err != nil {
			t.Fatalf("create environment telemetry: %v", err)
		}
	}
	fireRows := []*domain.TelemetryFire{
		{DeviceID: device.ID, FlameDetected: true, FlameLevel: intPtr(400), SmokeLevel: 100, Pump: true, Buzzer: false, RecordedAt: aggregateBase, ReceivedAt: aggregateBase},
		{DeviceID: device.ID, FlameDetected: false, FlameLevel: intPtr(600), SmokeLevel: 200, Pump: false, Buzzer: true, RecordedAt: aggregateBase.Add(time.Minute), ReceivedAt: aggregateBase},
		{DeviceID: device.ID, FlameDetected: true, FlameLevel: intPtr(700), SmokeLevel: 300, Pump: true, Buzzer: false, RecordedAt: aggregateBase.Add(5 * time.Minute), ReceivedAt: aggregateBase},
		{DeviceID: device.ID, FlameDetected: true, FlameLevel: intPtr(900), SmokeLevel: 400, Pump: true, Buzzer: false, RecordedAt: aggregateBase.Add(6 * time.Minute), ReceivedAt: aggregateBase},
	}
	for _, row := range fireRows {
		if err := repo.CreateFire(context.Background(), row); err != nil {
			t.Fatalf("create fire telemetry: %v", err)
		}
	}

	aggregateFrom := aggregateBase.Format(time.RFC3339)
	aggregateTo := aggregateBase.Add(10 * time.Minute).Format(time.RFC3339)
	var envAggregate struct {
		Data struct {
			DeviceID        string    `json:"device_id"`
			From            time.Time `json:"from"`
			To              time.Time `json:"to"`
			IntervalMinutes int       `json:"interval_minutes"`
			BucketTimezone  string    `json:"bucket_timezone"`
			Points          []struct {
				BucketStart  time.Time `json:"bucket_start"`
				Temperature  *float64  `json:"temperature"`
				Humidity     *float64  `json:"humidity"`
				SoilMoisture float64   `json:"soil_moisture"`
				SampleCount  int64     `json:"sample_count"`
			} `json:"points"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/telemetry/environment/aggregate?from="+aggregateFrom+"&to="+aggregateTo+"&interval=5", token, nil, &envAggregate)
	if status != http.StatusOK || envAggregate.Data.BucketTimezone != "Asia/Jakarta" || len(envAggregate.Data.Points) != 2 {
		t.Fatalf("environment aggregate status=%d tz=%q points=%d", status, envAggregate.Data.BucketTimezone, len(envAggregate.Data.Points))
	}
	firstEnv := envAggregate.Data.Points[0]
	if !firstEnv.BucketStart.Equal(aggregateBase) || firstEnv.Temperature == nil || *firstEnv.Temperature != 25 || firstEnv.Humidity == nil || *firstEnv.Humidity != 65 || firstEnv.SoilMoisture != 150 || firstEnv.SampleCount != 2 {
		t.Fatalf("environment bucket 1 = %+v", firstEnv)
	}
	secondEnv := envAggregate.Data.Points[1]
	if !secondEnv.BucketStart.Equal(aggregateBase.Add(5*time.Minute)) || secondEnv.Temperature == nil || *secondEnv.Temperature != 50 || secondEnv.Humidity == nil || *secondEnv.Humidity != 90 || secondEnv.SoilMoisture != 350 || secondEnv.SampleCount != 2 {
		t.Fatalf("environment bucket 2 = %+v", secondEnv)
	}

	var fireAggregate struct {
		Data struct {
			Points []struct {
				BucketStart        time.Time `json:"bucket_start"`
				FlameLevel         *float64  `json:"flame_level"`
				SmokeLevel         float64   `json:"smoke_level"`
				FlameDetectedRatio float64   `json:"flame_detected_ratio"`
				PumpOnRatio        float64   `json:"pump_on_ratio"`
				BuzzerOnRatio      float64   `json:"buzzer_on_ratio"`
				SampleCount        int64     `json:"sample_count"`
			} `json:"points"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/telemetry/fire/aggregate?from="+aggregateFrom+"&to="+aggregateTo+"&interval=5", token, nil, &fireAggregate)
	if status != http.StatusOK || len(fireAggregate.Data.Points) != 2 {
		t.Fatalf("fire aggregate status=%d points=%d", status, len(fireAggregate.Data.Points))
	}
	firstFire := fireAggregate.Data.Points[0]
	if !firstFire.BucketStart.Equal(aggregateBase) || firstFire.FlameLevel == nil || *firstFire.FlameLevel != 500 || firstFire.SmokeLevel != 150 || firstFire.FlameDetectedRatio != 0.5 || firstFire.PumpOnRatio != 0.5 || firstFire.BuzzerOnRatio != 0.5 || firstFire.SampleCount != 2 {
		t.Fatalf("fire bucket 1 = %+v", firstFire)
	}
	secondFire := fireAggregate.Data.Points[1]
	if !secondFire.BucketStart.Equal(aggregateBase.Add(5*time.Minute)) || secondFire.FlameLevel == nil || *secondFire.FlameLevel != 800 || secondFire.SmokeLevel != 350 || secondFire.FlameDetectedRatio != 1 || secondFire.PumpOnRatio != 1 || secondFire.BuzzerOnRatio != 0 || secondFire.SampleCount != 2 {
		t.Fatalf("fire bucket 2 = %+v", secondFire)
	}

	var envRaw struct {
		Data struct {
			Mode            string `json:"mode"`
			Decimated       bool   `json:"decimated"`
			SourcePoints    int    `json:"source_points"`
			ReturnedPoints  int    `json:"returned_points"`
			IntervalMinutes int    `json:"interval_minutes"`
			Points          []struct {
				Temperature  *float64  `json:"temperature"`
				Humidity     *float64  `json:"humidity"`
				SoilMoisture float64   `json:"soil_moisture"`
				RecordedAt   time.Time `json:"recorded_at"`
			} `json:"points"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/telemetry/environment/aggregate?from="+aggregateFrom+"&to="+aggregateTo+"&interval=0", token, nil, &envRaw)
	if status != http.StatusOK || envRaw.Data.Mode != "raw" || envRaw.Data.Decimated || envRaw.Data.SourcePoints != 4 || envRaw.Data.ReturnedPoints != 4 || envRaw.Data.IntervalMinutes != 0 || len(envRaw.Data.Points) != 4 {
		t.Fatalf("environment raw status=%d body=%+v", status, envRaw.Data)
	}
	if envRaw.Data.Points[0].Temperature == nil || *envRaw.Data.Points[0].Temperature != 20 || envRaw.Data.Points[3].Temperature == nil || *envRaw.Data.Points[3].Temperature != 50 {
		t.Fatalf("environment raw points=%+v", envRaw.Data.Points)
	}

	var fireRaw struct {
		Data struct {
			Mode           string `json:"mode"`
			Decimated      bool   `json:"decimated"`
			SourcePoints   int    `json:"source_points"`
			ReturnedPoints int    `json:"returned_points"`
			Points         []struct {
				FlameDetected bool      `json:"flame_detected"`
				SmokeLevel    int       `json:"smoke_level"`
				RecordedAt    time.Time `json:"recorded_at"`
			} `json:"points"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/telemetry/fire/aggregate?from="+aggregateFrom+"&to="+aggregateTo+"&interval=0", token, nil, &fireRaw)
	if status != http.StatusOK || fireRaw.Data.Mode != "raw" || fireRaw.Data.Decimated || fireRaw.Data.SourcePoints != 4 || fireRaw.Data.ReturnedPoints != 4 || len(fireRaw.Data.Points) != 4 {
		t.Fatalf("fire raw status=%d body=%+v", status, fireRaw.Data)
	}
	if !fireRaw.Data.Points[0].FlameDetected || fireRaw.Data.Points[0].SmokeLevel != 100 || fireRaw.Data.Points[3].SmokeLevel != 400 {
		t.Fatalf("fire raw points=%+v", fireRaw.Data.Points)
	}

	nowTouch := time.Now().UTC()
	if err := repo.TouchDevice(context.Background(), "TEST-001", nowTouch); err != nil {
		t.Fatalf("touch device: %v", err)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/status", token, nil, &deviceStatus)
	if status != http.StatusOK || deviceStatus.Data.Status != "online" || deviceStatus.Data.OnlineSince == nil || deviceStatus.Data.UptimeSeconds < 0 {
		t.Fatalf("online uptime status=%q uptime=%d online_since=%v", deviceStatus.Data.Status, deviceStatus.Data.UptimeSeconds, deviceStatus.Data.OnlineSince)
	}
	if deviceStatus.Data.Device.OnlineSince == nil || deviceStatus.Data.Device.UptimeSeconds < 0 {
		t.Fatalf("nested online uptime=%d online_since=%v", deviceStatus.Data.Device.UptimeSeconds, deviceStatus.Data.Device.OnlineSince)
	}

	if _, err := repo.MarkStaleDevicesOffline(context.Background(), time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("mark stale devices offline: %v", err)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices/TEST-001/status", token, nil, &deviceStatus)
	if status != http.StatusOK || deviceStatus.Data.Status != "offline" || deviceStatus.Data.UptimeSeconds != 0 || deviceStatus.Data.OnlineSince != nil {
		t.Fatalf("reset uptime status=%q uptime=%d online_since=%v", deviceStatus.Data.Status, deviceStatus.Data.UptimeSeconds, deviceStatus.Data.OnlineSince)
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

	var eventDetail struct {
		Data struct {
			ID              string  `json:"id"`
			ResolvedBy      *string `json:"resolved_by"`
			TimelineTotal   int64   `json:"timeline_total"`
			TimelineHasMore bool    `json:"timeline_has_more"`
			TimelinePreview []struct {
				Kind  string `json:"kind"`
				Title string `json:"title"`
				Actor struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"actor"`
			} `json:"timeline_preview"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID, token, nil, &eventDetail)
	if status != http.StatusOK || eventDetail.Data.ID != eventID || eventDetail.Data.TimelineTotal != 2 || eventDetail.Data.TimelineHasMore || len(eventDetail.Data.TimelinePreview) != 2 {
		t.Fatalf("event detail initial timeline status=%d body=%+v", status, eventDetail.Data)
	}
	if eventDetail.Data.TimelinePreview[0].Kind != string(domain.TimelineEventCreated) || eventDetail.Data.TimelinePreview[0].Actor.Type != string(domain.TimelineActorSystem) || eventDetail.Data.TimelinePreview[0].Actor.Name != "Sistem" {
		t.Fatalf("event detail created timeline = %+v", eventDetail.Data.TimelinePreview[0])
	}
	if eventDetail.Data.TimelinePreview[1].Kind != string(domain.TimelineNotification) {
		t.Fatalf("event detail notification timeline = %+v", eventDetail.Data.TimelinePreview[1])
	}

	var acked struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	status = doJSON(t, http.MethodPost, srv.URL+"/api/v1/events/"+eventID+"/acknowledge", token, nil, &acked)
	if status != http.StatusOK || acked.Data.Status != string(domain.EventAcknowledged) {
		t.Fatalf("ack status=%d event_status=%q", status, acked.Data.Status)
	}

	var timeline struct {
		Data []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Actor struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"actor"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID+"/timeline?page=1&per_page=1", token, nil, &timeline)
	if status != http.StatusOK || timeline.Meta.Total != 3 || len(timeline.Data) != 1 || timeline.Data[0].Kind != string(domain.TimelineEventCreated) {
		t.Fatalf("event timeline pagination status=%d body=%+v", status, timeline)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID+"/timeline", token, nil, &timeline)
	if status != http.StatusOK || timeline.Meta.Total != 3 || len(timeline.Data) != 3 {
		t.Fatalf("event timeline after ack status=%d body=%+v", status, timeline)
	}
	if timeline.Data[2].Kind != string(domain.TimelineAcknowledged) || timeline.Data[2].Actor.Type != string(domain.TimelineActorUser) || timeline.Data[2].Actor.Name != "Administrator" {
		t.Fatalf("event ack timeline = %+v", timeline.Data[2])
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID+"/timeline?include=important", token, nil, &timeline)
	if status != http.StatusOK || timeline.Meta.Total != 2 || len(timeline.Data) != 2 {
		t.Fatalf("event timeline important status=%d body=%+v", status, timeline)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID+"/timeline?include=invalid", token, nil, nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("event timeline invalid include status=%d", status)
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

	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID, token, nil, &eventDetail)
	if status != http.StatusOK || eventDetail.Data.ResolvedBy == nil || *eventDetail.Data.ResolvedBy == "" || eventDetail.Data.TimelineTotal != 4 || eventDetail.Data.TimelineHasMore {
		t.Fatalf("event detail resolved timeline status=%d body=%+v", status, eventDetail.Data)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+eventID+"/timeline", token, nil, &timeline)
	if status != http.StatusOK || timeline.Meta.Total != 4 || len(timeline.Data) != 4 {
		t.Fatalf("event timeline after resolve status=%d body=%+v", status, timeline)
	}
	if timeline.Data[3].Kind != string(domain.TimelineResolved) || timeline.Data[3].Actor.Type != string(domain.TimelineActorUser) || timeline.Data[3].Actor.Name != "Petugas Integration" {
		t.Fatalf("event resolved timeline = %+v", timeline.Data[3])
	}

	autoEvent, err := risk.EmitDeviceEvent(context.Background(), device, domain.EventSensorError, "integration", "Sensor integration error")
	if err != nil || autoEvent == nil {
		t.Fatalf("emit sensor event event=%+v err=%v", autoEvent, err)
	}
	updated, err := repo.ResolveActiveEventByType(context.Background(), device.ID, domain.EventSensorError)
	if err != nil || !updated {
		t.Fatalf("auto resolve updated=%v err=%v", updated, err)
	}
	status = doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/"+autoEvent.ID.String()+"/timeline", token, nil, &timeline)
	if status != http.StatusOK || timeline.Meta.Total != 2 || len(timeline.Data) != 2 {
		t.Fatalf("auto resolve timeline status=%d body=%+v", status, timeline)
	}
	if timeline.Data[1].Kind != string(domain.TimelineAutoResolved) || timeline.Data[1].Actor.Type != string(domain.TimelineActorSystem) || timeline.Data[1].Actor.Name != "Sistem" {
		t.Fatalf("auto resolved timeline = %+v", timeline.Data[1])
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
	if status != http.StatusOK || dashboard.Data.Devices.Total != 2 || dashboard.Data.ActiveEvents.Total != 0 {
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

func floatPtr(v float64) *float64 { return &v }

func intPtr(v int) *int { return &v }

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
