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
	t.Setenv("FCM_MIN_SEVERITY", "critical")
	t.Setenv("AUTO_PUMP_DURATION", "30s")
	t.Setenv("AUTO_PUMP_MAX_DURATION", "300s")
	t.Setenv("DEVICE_OFFLINE_AFTER", "30s")
	t.Setenv("MQTT_QOS", "1")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.TemperatureHighC != 40 || cfg.SoilDryPct != 30 || cfg.SmokeLevelMax != 300 {
		t.Fatalf("unexpected threshold defaults: %+v", cfg)
	}
	if cfg.AccessTTL != 15*time.Minute || cfg.RefreshTTL != 7*24*time.Hour {
		t.Fatalf("unexpected token ttl: %s %s", cfg.AccessTTL, cfg.RefreshTTL)
	}
	if cfg.AutoPumpDuration != 30*time.Second {
		t.Fatalf("AutoPumpDuration = %s", cfg.AutoPumpDuration)
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
		{"invalid mqtt qos", "MQTT_QOS", "3"},
		{"auto pump longer than max", "AUTO_PUMP_DURATION", "600s"},
		{"offline threshold too low", "DEVICE_OFFLINE_AFTER", "1s"},
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

func TestLoadRequiresDatabase(t *testing.T) {
	setRequired(t)
	t.Setenv("DB_DSN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DB_DSN is missing")
	}
}
