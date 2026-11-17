package repository

import (
	"testing"
	"time"

	"firefreeforest/backend/internal/domain"
)

func TestDecimateEnvironmentPreservesExtrema(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]domain.TelemetryEnvironment, 6000)
	for i := range rows {
		temperature := float64(i)
		humidity := float64(len(rows) - 1 - i)
		soil := float64(len(rows) - 1 - i)
		rows[i] = domain.TelemetryEnvironment{
			ID:           uint64(i + 1),
			Temperature:  &temperature,
			Humidity:     &humidity,
			SoilMoisture: soil,
			RecordedAt:   base.Add(time.Duration(i) * time.Second),
			ReceivedAt:   base,
		}
	}

	got := decimateEnvironment(rows)
	if len(got) > rawMaxOutputPoints {
		t.Fatalf("returned %d points, want <= %d", len(got), rawMaxOutputPoints)
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.Temperature != nil && *row.Temperature == 0
	}) {
		t.Fatal("minimum temperature was not preserved")
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.Temperature != nil && *row.Temperature == float64(len(rows)-1)
	}) {
		t.Fatal("maximum temperature was not preserved")
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.Humidity != nil && *row.Humidity == 0
	}) {
		t.Fatal("minimum humidity was not preserved")
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.Humidity != nil && *row.Humidity == float64(len(rows)-1)
	}) {
		t.Fatal("maximum humidity was not preserved")
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.SoilMoisture == 0
	}) {
		t.Fatal("minimum soil moisture was not preserved")
	}
	if !hasEnvironmentValue(got, func(row domain.TelemetryEnvironment) bool {
		return row.SoilMoisture == float64(len(rows)-1)
	}) {
		t.Fatal("maximum soil moisture was not preserved")
	}
	assertEnvironmentSorted(t, got)
}

func TestDecimateFirePreservesExtremaAndFlame(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]domain.TelemetryFire, 6000)
	for i := range rows {
		rows[i] = domain.TelemetryFire{
			ID:            uint64(i + 1),
			SmokeLevel:    i,
			FlameDetected: i == 3333,
			RecordedAt:    base.Add(time.Duration(i) * time.Second),
			ReceivedAt:    base,
		}
	}

	got := decimateFire(rows)
	if len(got) > rawMaxOutputPoints {
		t.Fatalf("returned %d points, want <= %d", len(got), rawMaxOutputPoints)
	}
	if !hasFireValue(got, func(row domain.TelemetryFire) bool { return row.SmokeLevel == 0 }) {
		t.Fatal("minimum smoke level was not preserved")
	}
	if !hasFireValue(got, func(row domain.TelemetryFire) bool { return row.SmokeLevel == len(rows)-1 }) {
		t.Fatal("maximum smoke level was not preserved")
	}
	if !hasFireValue(got, func(row domain.TelemetryFire) bool { return row.FlameDetected }) {
		t.Fatal("flame_detected row was not preserved")
	}
	assertFireSorted(t, got)
}

func hasEnvironmentValue(rows []domain.TelemetryEnvironment, match func(domain.TelemetryEnvironment) bool) bool {
	for _, row := range rows {
		if match(row) {
			return true
		}
	}
	return false
}

func hasFireValue(rows []domain.TelemetryFire, match func(domain.TelemetryFire) bool) bool {
	for _, row := range rows {
		if match(row) {
			return true
		}
	}
	return false
}

func assertEnvironmentSorted(t *testing.T, rows []domain.TelemetryEnvironment) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		if rows[i].RecordedAt.Before(rows[i-1].RecordedAt) {
			t.Fatalf("rows not sorted at index %d", i)
		}
	}
}

func assertFireSorted(t *testing.T, rows []domain.TelemetryFire) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		if rows[i].RecordedAt.Before(rows[i-1].RecordedAt) {
			t.Fatalf("rows not sorted at index %d", i)
		}
	}
}
