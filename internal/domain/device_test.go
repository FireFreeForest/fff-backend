package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeviceUptimeSeconds(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	since := now.Add(-90 * time.Second)

	if got := (Device{Status: DeviceOnline, OnlineSince: &since}).UptimeSeconds(now); got != 90 {
		t.Fatalf("online uptime = %d, want 90", got)
	}
	if got := (Device{Status: DeviceOffline, OnlineSince: &since}).UptimeSeconds(now); got != 0 {
		t.Fatalf("offline uptime = %d, want 0", got)
	}
	if got := (Device{Status: DeviceOnline}).UptimeSeconds(now); got != 0 {
		t.Fatalf("online without online_since uptime = %d, want 0", got)
	}
	if got := (Device{Status: DeviceOnline, OnlineSince: &now}).UptimeSeconds(now.Add(-time.Second)); got != 0 {
		t.Fatalf("future online_since uptime = %d, want 0", got)
	}
}

func TestDeviceJSONIncludesOnlineSinceAndUptime(t *testing.T) {
	since := time.Now().UTC().Add(-2 * time.Minute)
	raw, err := json.Marshal(Device{DeviceID: "TEST-001", Status: DeviceOnline, OnlineSince: &since})
	if err != nil {
		t.Fatalf("marshal device: %v", err)
	}
	var got struct {
		DeviceID      string     `json:"device_id"`
		OnlineSince   *time.Time `json:"online_since"`
		UptimeSeconds int64      `json:"uptime_seconds"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal device: %v", err)
	}
	if got.DeviceID != "TEST-001" || got.OnlineSince == nil || got.UptimeSeconds < 119 {
		t.Fatalf("device json = %+v body=%s", got, raw)
	}
}
