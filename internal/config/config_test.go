package config

import (
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DSN", "host=localhost user=fff dbname=fff port=5432 sslmode=disable")
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("AUTO_PUMP_DURATION", "30s")
	t.Setenv("AUTO_PUMP_MAX_DURATION", "300s")
	t.Setenv("DEVICE_OFFLINE_AFTER", "30s")
	t.Setenv("MQTT_QOS", "1")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("MCP_RATE_LIMIT_PER_MIN", "")
	t.Setenv("MCP_API_KEY", "")
	t.Setenv("MASTRA_URL", "")
	t.Setenv("ASSISTANT_RATE_LIMIT_PER_MIN", "")
	t.Setenv("ASSISTANT_MAX_CONCURRENT_STREAMS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.TemperatureHighC != 40 || cfg.SoilDryADC != 300 || cfg.SmokeLevelMax != 500 || cfg.BatteryLowPercent != 20 {
		t.Fatalf("unexpected threshold defaults: %+v", cfg)
	}
	if cfg.FCMMinSeverity != "error" {
		t.Fatalf("FCMMinSeverity = %q", cfg.FCMMinSeverity)
	}
	wantEvents := "FIRE_DETECTED,SMOKE_DETECTED,TEMPERATURE_HIGH,SOIL_DRY,DEVICE_OFFLINE,SENSOR_ERROR,LOW_BATTERY"
	if got := strings.Join(cfg.FCMEventTypes, ","); got != wantEvents {
		t.Fatalf("FCMEventTypes = %q", got)
	}
	if cfg.MQTTTopicPrefix != "fire-free-forest" {
		t.Fatalf("MQTTTopicPrefix = %q", cfg.MQTTTopicPrefix)
	}
	if cfg.AutoPumpEnabled {
		t.Fatal("auto pump should be disabled by default; the device owns pump automation")
	}
	if cfg.AccessTTL != 15*time.Minute || cfg.RefreshTTL != 7*24*time.Hour {
		t.Fatalf("unexpected token ttl: %s %s", cfg.AccessTTL, cfg.RefreshTTL)
	}
	if cfg.AutoPumpDuration != 30*time.Second {
		t.Fatalf("AutoPumpDuration = %s", cfg.AutoPumpDuration)
	}
	if cfg.MCPAPIKey != "" {
		t.Fatal("MCP API key should be empty by default")
	}
	if cfg.MCPRateLimitPerMin != 120 {
		t.Fatalf("unexpected MCP default rate=%d", cfg.MCPRateLimitPerMin)
	}
	if cfg.MastraURL != "" {
		t.Fatalf("MastraURL should be empty by default, got %q", cfg.MastraURL)
	}
	if cfg.AssistantRateLimitPerMin != 20 {
		t.Fatalf("AssistantRateLimitPerMin = %d, want 20", cfg.AssistantRateLimitPerMin)
	}
	if cfg.AssistantMaxConcurrent != 2 {
		t.Fatalf("AssistantMaxConcurrent = %d, want 2", cfg.AssistantMaxConcurrent)
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{"short jwt secret", "JWT_SECRET", "short"},
		{"invalid fcm severity", "FCM_MIN_SEVERITY", "info"},
		{"invalid fcm event type", "FCM_EVENT_TYPES", "FIRE_DETECTED,INVALID"},
		{"invalid mqtt qos", "MQTT_QOS", "3"},
		{"invalid mqtt prefix", "MQTT_TOPIC_PREFIX", "free/flame"},
		{"auto pump longer than max", "AUTO_PUMP_DURATION", "600s"},
		{"offline threshold too low", "DEVICE_OFFLINE_AFTER", "1s"},
		{"negative mcp rate limit", "MCP_RATE_LIMIT_PER_MIN", "-1"},
		{"negative assistant rate limit", "ASSISTANT_RATE_LIMIT_PER_MIN", "-1"},
		{"negative assistant concurrent", "ASSISTANT_MAX_CONCURRENT_STREAMS", "-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.val)
			}
		})
	}
}

func TestLoadNormalizesFCMEventTypes(t *testing.T) {
	setRequired(t)
	t.Setenv("FCM_EVENT_TYPES", " fire_detected,SMOKE_DETECTED,FIRE_DETECTED, temperature_high ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := "FIRE_DETECTED,SMOKE_DETECTED,TEMPERATURE_HIGH"
	if got := strings.Join(cfg.FCMEventTypes, ","); got != want {
		t.Fatalf("FCMEventTypes = %q, want %q", got, want)
	}
}

func TestLoadAssistantConfig(t *testing.T) {
	setRequired(t)
	t.Setenv("MASTRA_URL", "http://127.0.0.1:4111")
	t.Setenv("ASSISTANT_RATE_LIMIT_PER_MIN", "35")
	t.Setenv("ASSISTANT_MAX_CONCURRENT_STREAMS", "4")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MastraURL != "http://127.0.0.1:4111" {
		t.Fatalf("MastraURL = %q", cfg.MastraURL)
	}
	if cfg.AssistantRateLimitPerMin != 35 || cfg.AssistantMaxConcurrent != 4 {
		t.Fatalf("unexpected assistant limits: rate=%d concurrent=%d", cfg.AssistantRateLimitPerMin, cfg.AssistantMaxConcurrent)
	}
}

func TestLoadAssistantZeroLimitsAreValid(t *testing.T) {
	setRequired(t)
	t.Setenv("ASSISTANT_RATE_LIMIT_PER_MIN", "0")
	t.Setenv("ASSISTANT_MAX_CONCURRENT_STREAMS", "0")
	if _, err := Load(); err != nil {
		t.Fatalf("zero limits should be valid, got %v", err)
	}
}

func TestLoadRequiresDatabase(t *testing.T) {
	setRequired(t)
	t.Setenv("DB_DSN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DB_DSN is missing")
	}
}

func TestLoadMCPDoesNotRequireJWT(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_SECRET", "")
	cfg, err := LoadMCP()
	if err != nil {
		t.Fatalf("LoadMCP: %v", err)
	}
	if cfg.MCPRateLimitPerMin != 120 {
		t.Fatalf("MCPRateLimitPerMin = %d", cfg.MCPRateLimitPerMin)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load should still require JWT_SECRET")
	}
}

func TestLoadMigrateDoesNotRequireJWT(t *testing.T) {
	setRequired(t)
	t.Setenv("JWT_SECRET", "")
	cfg, err := LoadMigrate()
	if err != nil {
		t.Fatalf("LoadMigrate: %v", err)
	}
	if cfg.DBDSN == "" {
		t.Fatal("DBDSN should be set")
	}
}
