package service

import (
	"context"
	"testing"

	"firefreeforest/backend/internal/domain"
)

func TestNotificationChannel(t *testing.T) {
	tests := []struct {
		eventType domain.EventType
		wantID    string
		wantSound string
	}{
		{eventType: domain.EventFireDetected, wantID: "fire_alerts_fire", wantSound: "notif_fire"},
		{eventType: domain.EventSmokeDetected, wantID: "fire_alerts_warning", wantSound: "notif_smoke"},
		{eventType: domain.EventTemperatureHigh, wantID: "fire_alerts_warning", wantSound: "notif_smoke"},
		{eventType: domain.EventSoilDry, wantID: "fire_alerts_warning", wantSound: "notif_smoke"},
		{eventType: domain.EventDeviceOffline, wantID: "fire_alerts_error", wantSound: "notif_sensor_error"},
		{eventType: domain.EventSensorError, wantID: "fire_alerts_error", wantSound: "notif_sensor_error"},
	}
	for _, tc := range tests {
		t.Run(string(tc.eventType), func(t *testing.T) {
			gotID, gotSound := notificationChannel(tc.eventType)
			if gotID != tc.wantID || gotSound != tc.wantSound {
				t.Fatalf("notificationChannel(%s) = (%s, %s), want (%s, %s)", tc.eventType, gotID, gotSound, tc.wantID, tc.wantSound)
			}
		})
	}
}

func TestNotifierAllowsEvent(t *testing.T) {
	tests := []struct {
		name       string
		eventTypes []string
		eventType  domain.EventType
		want       bool
	}{
		{name: "empty allows all", eventType: domain.EventFireDetected, want: true},
		{name: "allowed event", eventTypes: []string{"fire_detected"}, eventType: domain.EventFireDetected, want: true},
		{name: "denied event", eventTypes: []string{"FIRE_DETECTED"}, eventType: domain.EventSmokeDetected, want: false},
		{name: "invalid entries ignored", eventTypes: []string{"INVALID", "SMOKE_DETECTED"}, eventType: domain.EventSmokeDetected, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notifier := NewNotifier(nil, nil, domain.SeverityError, tc.eventTypes, nil)
			if got := notifier.allowsEvent(tc.eventType); got != tc.want {
				t.Fatalf("allowsEvent(%s) = %v, want %v", tc.eventType, got, tc.want)
			}
		})
	}
}

func TestNotificationOutcome(t *testing.T) {
	tests := []struct {
		name       string
		sent       int
		err        error
		want       domain.EventTimelineOutcome
		wantReason string
	}{
		{name: "sent", sent: 2, want: domain.TimelineOutcomeSent, wantReason: "delivered"},
		{name: "partial", sent: 1, err: context.DeadlineExceeded, want: domain.TimelineOutcomePartial, wantReason: "partial_delivery"},
		{name: "failed", err: context.DeadlineExceeded, want: domain.TimelineOutcomeFailed, wantReason: "send_failed"},
		{name: "no delivery", want: domain.TimelineOutcomeFailed, wantReason: "no_delivery"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := notificationOutcome(tc.sent, tc.err)
			if got != tc.want || reason != tc.wantReason {
				t.Fatalf("notificationOutcome(%d, %v) = (%s, %s), want (%s, %s)", tc.sent, tc.err, got, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestNotificationTimelineTitle(t *testing.T) {
	tests := []struct {
		kind    domain.EventTimelineKind
		outcome domain.EventTimelineOutcome
		want    string
	}{
		{kind: domain.TimelineNotification, outcome: domain.TimelineOutcomeSent, want: "Peringatan Dikirim"},
		{kind: domain.TimelineNotification, outcome: domain.TimelineOutcomeFailed, want: "Peringatan Gagal Dikirim"},
		{kind: domain.TimelineReminder, outcome: domain.TimelineOutcomeSkipped, want: "Peringatan Ulang Dilewati"},
		{kind: domain.TimelineEscalation, outcome: domain.TimelineOutcomePartial, want: "Eskalasi Darurat Terkirim Sebagian"},
	}
	for _, tc := range tests {
		if got := domain.NotificationTimelineTitle(tc.kind, tc.outcome); got != tc.want {
			t.Fatalf("NotificationTimelineTitle(%s, %s) = %q, want %q", tc.kind, tc.outcome, got, tc.want)
		}
	}
}
