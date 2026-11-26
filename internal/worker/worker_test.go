package worker

import (
	"testing"
	"time"
)

func TestTopicDeviceID(t *testing.T) {
	const prefix = "fire-free-forest"
	tests := []struct {
		topic  string
		want   string
		wantOK bool
	}{
		{"fire-free-forest/FFF-VIRTUAL-DEVICE/telemetry/fire", "FFF-VIRTUAL-DEVICE", true},
		{"fire-free-forest/abc/event", "abc", true},
		{"free-flame-forest/FFF-VIRTUAL-DEVICE/telemetry/fire", "", false},
		{"other/FFF-VIRTUAL-DEVICE/telemetry/fire", "", false},
		{"fire-free-forest", "", false},
		{"fire-free-forest//event", "", false},
		{"fire-free-forest/+/event", "", false},
		{"fire-free-forest/DEV#/event", "", false},
	}
	for _, tc := range tests {
		got, ok := topicDeviceID(tc.topic, prefix)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("topicDeviceID(%q) = %q,%v want %q,%v", tc.topic, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestADCInRange(t *testing.T) {
	tests := []struct {
		value float64
		want  bool
	}{
		{-1, false},
		{0, true},
		{1023, true},
		{1024, true},
		{1025, false},
	}
	for _, tc := range tests {
		if got := adcInRange(tc.value); got != tc.want {
			t.Errorf("adcInRange(%v) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func newDeviceTelemetry(deviceID string, rssi *int, percentage *int, voltage *float64) deviceTelemetryPayload {
	var p deviceTelemetryPayload
	p.DeviceID = deviceID
	p.Wifi.RSSI = rssi
	p.Battery.Percentage = percentage
	p.Battery.Voltage = voltage
	return p
}

func TestValidateDeviceTelemetry(t *testing.T) {
	tests := []struct {
		name       string
		topicID    string
		payload    deviceTelemetryPayload
		wantSource string
		wantMsg    string
	}{
		{
			name:    "accepts complete payload",
			topicID: "DEV-1",
			payload: newDeviceTelemetry("DEV-1", intPtr(-55), intPtr(90), floatPtr(4.0)),
		},
		{
			name:    "accepts payload without optional fields",
			topicID: "DEV-1",
			payload: newDeviceTelemetry("DEV-1", nil, nil, nil),
		},
		{
			name:    "accepts payload without device_id",
			topicID: "DEV-1",
			payload: newDeviceTelemetry("", intPtr(-55), intPtr(90), floatPtr(4.0)),
		},
		{
			name:       "rejects device_id that does not match the topic",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-2", intPtr(-55), intPtr(90), floatPtr(4.0)),
			wantSource: "device_id_mismatch",
			wantMsg:    "device_id pada payload telemetri perangkat tidak sesuai dengan topik MQTT.",
		},
		{
			name:    "accepts RSSI bounds",
			topicID: "DEV-1",
			payload: newDeviceTelemetry("DEV-1", intPtr(0), intPtr(0), floatPtr(0)),
		},
		{
			name:    "accepts weakest RSSI",
			topicID: "DEV-1",
			payload: newDeviceTelemetry("DEV-1", intPtr(-120), intPtr(100), floatPtr(6)),
		},
		{
			name:       "rejects RSSI above zero",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", intPtr(1), nil, nil),
			wantSource: "value_out_of_range",
			wantMsg:    "RSSI WiFi tidak valid: 1",
		},
		{
			name:       "rejects RSSI below -120",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", intPtr(-121), nil, nil),
			wantSource: "value_out_of_range",
			wantMsg:    "RSSI WiFi tidak valid: -121",
		},
		{
			name:       "rejects negative battery percentage",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", nil, intPtr(-1), nil),
			wantSource: "value_out_of_range",
			wantMsg:    "Persentase baterai tidak valid: -1",
		},
		{
			name:       "rejects battery percentage above 100",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", nil, intPtr(101), nil),
			wantSource: "value_out_of_range",
			wantMsg:    "Persentase baterai tidak valid: 101",
		},
		{
			name:       "rejects negative battery voltage",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", nil, nil, floatPtr(-0.1)),
			wantSource: "value_out_of_range",
			wantMsg:    "Tegangan baterai tidak valid: -0.100",
		},
		{
			name:       "rejects battery voltage above six volts",
			topicID:    "DEV-1",
			payload:    newDeviceTelemetry("DEV-1", nil, nil, floatPtr(6.1)),
			wantSource: "value_out_of_range",
			wantMsg:    "Tegangan baterai tidak valid: 6.100",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issue := validateDeviceTelemetry(tc.topicID, tc.payload)
			if tc.wantSource == "" {
				if issue != nil {
					t.Fatalf("unexpected issue: %+v", issue)
				}
				return
			}
			if issue == nil {
				t.Fatalf("expected %s issue, got nil", tc.wantSource)
			}
			if issue.Source != tc.wantSource {
				t.Fatalf("source = %s, want %s", issue.Source, tc.wantSource)
			}
			if issue.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", issue.Message, tc.wantMsg)
			}
		})
	}
}

func TestParseRecordedAt(t *testing.T) {
	before := time.Now().UTC()
	if got := parseRecordedAt(""); got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("empty timestamp should fall back to now, got %s", got)
	}
	if got := parseRecordedAt("not-a-time"); got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("invalid timestamp should fall back to now, got %s", got)
	}

	past := "2025-01-02T03:04:05Z"
	if got := parseRecordedAt(past); got.Format(time.RFC3339) != past {
		t.Fatalf("past timestamp = %s, want %s", got.Format(time.RFC3339), past)
	}

	future := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	got := parseRecordedAt(future)
	if got.After(time.Now().Add(time.Minute)) {
		t.Fatalf("future timestamp should be capped, got %s", got)
	}
}

func intPtr(v int) *int { return &v }

func floatPtr(v float64) *float64 { return &v }
