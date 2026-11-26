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
	t.Setenv("MCP_ENABLED", "")
	t.Setenv("MCP_ROLE", "")
	t.Setenv("MCP_RATE_LIMIT_PER_MIN", "")
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
	if cfg.MCPEnabled {
		t.Fatal("MCP should be disabled by default")
	}
	if cfg.MCPRateLimitPerMin != 120 || cfg.MCPRole != "viewer" {
		t.Fatalf("unexpected MCP defaults: rate=%d role=%q", cfg.MCPRateLimitPerMin, cfg.MCPRole)
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
		{"invalid mcp role", "MCP_ROLE", "operator"},
		{"negative mcp rate limit", "MCP_RATE_LIMIT_PER_MIN", "-1"},
		{"invalid mcp actor id", "MCP_ACTOR_ID", "not-a-uuid"},
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
	if cfg.MCPRole != "viewer" {
		t.Fatalf("MCPRole = %q", cfg.MCPRole)
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
