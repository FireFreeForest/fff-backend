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
