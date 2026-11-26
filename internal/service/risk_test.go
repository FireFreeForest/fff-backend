package service

import (
	"testing"

	"firefreeforest/backend/internal/domain"
)

func environment(temperature, humidity, soil float64) *domain.TelemetryEnvironment {
	return &domain.TelemetryEnvironment{
		Temperature:  &temperature,
		Humidity:     &humidity,
		SoilMoisture: soil,
	}
}

func fire(flame bool, smoke int) *domain.TelemetryFire {
	return &domain.TelemetryFire{FlameDetected: flame, SmokeLevel: smoke}
}

func device(battery int) *domain.TelemetryDevice {
	return &domain.TelemetryDevice{BatteryPercentage: &battery}
}

func TestEvaluateConditions(t *testing.T) {
	th := Thresholds{TemperatureHighC: 40, SoilDryADC: 300, SmokeLevelMax: 500, BatteryLowPercent: 20}

	tests := []struct {
		name      string
		state     LatestState
		wantLevel string
		wantEvent []domain.EventType
	}{
		{
			name:      "normal when all sensors below thresholds",
			state:     LatestState{Environment: environment(31, 60, 640), Fire: fire(false, 120)},
			wantLevel: RiskNormal,
		},
		{
			name:      "temperature at website threshold is waspada",
			state:     LatestState{Environment: environment(40, 40, 640), Fire: fire(false, 120)},
			wantLevel: RiskWaspada,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh},
		},
		{
			name:      "two warnings become bahaya",
			state:     LatestState{Environment: environment(40, 40, 20), Fire: fire(false, 120)},
			wantLevel: RiskBahaya,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh, domain.EventSoilDry},
		},
		{
			name:      "flame detected is darurat",
			state:     LatestState{Environment: environment(31, 60, 640), Fire: fire(true, 120)},
			wantLevel: RiskDarurat,
			wantEvent: []domain.EventType{domain.EventFireDetected},
		},
		{
			name:      "smoke at website threshold is warning",
			state:     LatestState{Environment: environment(31, 60, 640), Fire: fire(false, 500)},
			wantLevel: RiskWaspada,
			wantEvent: []domain.EventType{domain.EventSmokeDetected},
		},
		{
			name:      "exact website thresholds are active",
			state:     LatestState{Environment: environment(40, 60, 640), Fire: fire(false, 500)},
			wantLevel: RiskBahaya,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh, domain.EventSmokeDetected},
		},
		{
			name:      "fire only without environment still evaluates",
			state:     LatestState{Fire: fire(true, 900)},
			wantLevel: RiskDarurat,
			wantEvent: []domain.EventType{domain.EventFireDetected, domain.EventSmokeDetected},
		},
		{
			name:      "partial environment from sensor error is accepted",
			state:     LatestState{Environment: &domain.TelemetryEnvironment{SoilMoisture: 640}, Fire: fire(false, 120)},
			wantLevel: RiskNormal,
		},
		{
			name:      "low battery raises a warning",
			state:     LatestState{Device: device(18)},
			wantLevel: RiskWaspada,
			wantEvent: []domain.EventType{domain.EventLowBattery},
		},
		{
			name:      "battery exactly at threshold is still healthy",
			state:     LatestState{Device: device(20)},
			wantLevel: RiskNormal,
		},
		{
			name:      "healthy battery stays normal",
			state:     LatestState{Device: device(90)},
			wantLevel: RiskNormal,
		},
		{
			name:      "low battery adds to sensor warnings",
			state:     LatestState{Environment: environment(45, 60, 640), Device: device(10)},
			wantLevel: RiskBahaya,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh, domain.EventLowBattery},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conditions, level := EvaluateConditions(tc.state, th)
			if level != tc.wantLevel {
				t.Fatalf("level = %s, want %s", level, tc.wantLevel)
			}
			active := make(map[domain.EventType]bool)
			for _, c := range conditions {
				if c.Active {
					active[c.Type] = true
				}
			}
			if len(active) != len(tc.wantEvent) {
				t.Fatalf("active events = %v, want %v", active, tc.wantEvent)
			}
			for _, want := range tc.wantEvent {
				if !active[want] {
					t.Fatalf("expected active event %s, got %v", want, active)
				}
			}
		})
	}
}

func TestEvaluateConditionsSeverity(t *testing.T) {
	th := Thresholds{TemperatureHighC: 40, SoilDryADC: 300, SmokeLevelMax: 500, BatteryLowPercent: 20}
	conditions, _ := EvaluateConditions(LatestState{
		Environment: environment(45, 30, 10),
		Fire:        fire(true, 800),
		Device:      device(5),
	}, th)

	severities := make(map[domain.EventType]domain.Severity)
	for _, c := range conditions {
		if c.Active {
			severities[c.Type] = c.Severity
		}
	}
	if severities[domain.EventFireDetected] != domain.SeverityCritical {
		t.Fatalf("fire severity = %s, want critical", severities[domain.EventFireDetected])
	}
	for _, typ := range []domain.EventType{domain.EventTemperatureHigh, domain.EventSoilDry, domain.EventSmokeDetected, domain.EventLowBattery} {
		if severities[typ] != domain.SeverityWarning {
			t.Fatalf("%s severity = %s, want warning", typ, severities[typ])
		}
	}
}

func TestEvaluateConditionsBatteryThresholdDisabled(t *testing.T) {
	th := Thresholds{TemperatureHighC: 40, SoilDryADC: 300, SmokeLevelMax: 500}
	conditions, level := EvaluateConditions(LatestState{Device: device(1)}, th)
	if level != RiskNormal {
		t.Fatalf("level = %s, want %s when battery threshold is disabled", level, RiskNormal)
	}
	for _, c := range conditions {
		if c.Type == domain.EventLowBattery && c.Active {
			t.Fatal("battery condition must stay inactive when THRESHOLD_BATTERY_LOW_PERCENT is 0")
		}
	}
}

func TestStateMetadataIncludesDeviceTelemetry(t *testing.T) {
	rssi := -49
	voltage := 3.456729
	percentage := 18
	state := LatestState{
		Environment: environment(31.5, 60, 640),
		Fire:        fire(false, 120),
		Device: &domain.TelemetryDevice{
			FirmwareVersion:   "1.3.0",
			WifiRSSI:          &rssi,
			BatteryVoltage:    &voltage,
			BatteryPercentage: &percentage,
		},
	}

	meta := stateMetadata(state)
	checks := map[string]any{
		"temperature":        31.5,
		"humidity":           60.0,
		"soil_moisture":      640.0,
		"flame_detected":     false,
		"smoke_level":        120,
		"pump":               false,
		"firmware_version":   "1.3.0",
		"wifi_rssi":          -49,
		"battery_voltage":    3.456729,
		"battery_percentage": 18,
	}
	for key, want := range checks {
		got, ok := meta[key]
		if !ok {
			t.Fatalf("metadata missing %q: %+v", key, meta)
		}
		if got != want {
			t.Fatalf("metadata[%q] = %v, want %v", key, got, want)
		}
	}
}

func TestStateMetadataOmitsAbsentDeviceTelemetry(t *testing.T) {
	meta := stateMetadata(LatestState{Environment: environment(31.5, 60, 640), Fire: fire(false, 120)})
	for _, key := range []string{"firmware_version", "wifi_rssi", "battery_voltage", "battery_percentage"} {
		if _, ok := meta[key]; ok {
			t.Fatalf("metadata should omit %q when device telemetry is absent: %+v", key, meta)
		}
	}
}
