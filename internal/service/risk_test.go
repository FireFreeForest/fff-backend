package service

import (
	"testing"

	"firefreeforest/backend/internal/domain"
)

func environment(temperature, humidity, soil float64) *domain.TelemetryEnvironment {
	return &domain.TelemetryEnvironment{
		Temperature:  temperature,
		Humidity:     humidity,
		SoilMoisture: soil,
	}
}

func fire(flame bool, smoke int) *domain.TelemetryFire {
	return &domain.TelemetryFire{FlameDetected: flame, SmokeLevel: smoke}
}

func TestEvaluateConditions(t *testing.T) {
	th := Thresholds{TemperatureHighC: 40, SoilDryPct: 30, SmokeLevelMax: 300}

	tests := []struct {
		name      string
		state     LatestState
		wantLevel string
		wantEvent []domain.EventType
	}{
		{
			name:      "normal when all sensors below thresholds",
			state:     LatestState{Environment: environment(31, 60, 55), Fire: fire(false, 120)},
			wantLevel: RiskNormal,
		},
		{
			name:      "temperature above threshold is waspada",
			state:     LatestState{Environment: environment(42, 40, 55), Fire: fire(false, 120)},
			wantLevel: RiskWaspada,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh},
		},
		{
			name:      "two warnings become bahaya",
			state:     LatestState{Environment: environment(42, 40, 20), Fire: fire(false, 120)},
			wantLevel: RiskBahaya,
			wantEvent: []domain.EventType{domain.EventTemperatureHigh, domain.EventSoilDry},
		},
		{
			name:      "flame detected is darurat",
			state:     LatestState{Environment: environment(31, 60, 55), Fire: fire(true, 120)},
			wantLevel: RiskDarurat,
			wantEvent: []domain.EventType{domain.EventFireDetected},
		},
		{
			name:      "smoke above threshold is warning",
			state:     LatestState{Environment: environment(31, 60, 55), Fire: fire(false, 450)},
			wantLevel: RiskWaspada,
			wantEvent: []domain.EventType{domain.EventSmokeDetected},
		},
		{
			name:      "exact threshold is not active",
			state:     LatestState{Environment: environment(40, 60, 30), Fire: fire(false, 300)},
			wantLevel: RiskNormal,
		},
		{
			name:      "fire only without environment still evaluates",
			state:     LatestState{Fire: fire(true, 900)},
			wantLevel: RiskDarurat,
			wantEvent: []domain.EventType{domain.EventFireDetected, domain.EventSmokeDetected},
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
	th := Thresholds{TemperatureHighC: 40, SoilDryPct: 30, SmokeLevelMax: 300}
	conditions, _ := EvaluateConditions(LatestState{
		Environment: environment(45, 30, 10),
		Fire:        fire(true, 800),
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
	for _, typ := range []domain.EventType{domain.EventTemperatureHigh, domain.EventSoilDry, domain.EventSmokeDetected} {
		if severities[typ] != domain.SeverityWarning {
			t.Fatalf("%s severity = %s, want warning", typ, severities[typ])
		}
	}
}
