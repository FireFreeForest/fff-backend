package service

import (
	"context"
	"fmt"
	"log/slog"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/platform/fcm"
	"firefreeforest/backend/internal/repository"
)

type Notifier struct {
	fcm         *fcm.Client
	repo        *repository.Repository
	log         *slog.Logger
	minSeverity domain.Severity
}

func NewNotifier(fcmClient *fcm.Client, repo *repository.Repository, minSeverity domain.Severity, log *slog.Logger) *Notifier {
	return &Notifier{fcm: fcmClient, repo: repo, log: log, minSeverity: minSeverity}
}

func (n *Notifier) NotifyEvent(ctx context.Context, device *domain.Device, ev *domain.Event) {
	if n == nil || !n.fcm.Enabled() || ev == nil {
		return
	}
	if domain.SeverityRank(ev.Severity) < domain.SeverityRank(n.minSeverity) {
		return
	}

	tokens, err := n.repo.ListNotificationTokens(ctx, []domain.UserRole{domain.RoleAdmin, domain.RolePetugas})
	if err != nil {
		n.log.Warn("failed to load push tokens", "error", err)
		return
	}
	if len(tokens) == 0 {
		return
	}
	values := make([]string, 0, len(tokens))
	for i := range tokens {
		values = append(values, tokens[i].Token)
	}

	title, body := notificationText(device, ev)
	data := map[string]string{
		"event_id":     ev.ID.String(),
		"device_id":    device.DeviceID,
		"device_name":  device.Name,
		"event_type":   string(ev.EventType),
		"severity":     string(ev.Severity),
		"triggered_at": ev.TriggeredAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	invalid, sent, err := n.fcm.Send(ctx, values, title, body, data)
	if err != nil {
		n.log.Warn("fcm send partially failed", "device_id", device.DeviceID, "event_type", ev.EventType, "error", err)
		observability.NotificationsSent.WithLabelValues("error").Inc()
	} else {
		observability.NotificationsSent.WithLabelValues("sent").Inc()
		n.log.Info("fcm notifications sent",
			"device_id", device.DeviceID,
			"event_type", ev.EventType,
			"recipient_count", sent,
		)
	}
	if len(invalid) > 0 {
		if err := n.repo.DeleteInvalidPushTokens(ctx, invalid); err != nil {
			n.log.Warn("failed to delete invalid push tokens", "error", err)
		}
	}
}

// NotifyFireFollowUp sends a reminder or escalation for an unacknowledged fire event.
func (n *Notifier) NotifyFireFollowUp(ctx context.Context, device *domain.Device, ev *domain.Event, escalation bool) bool {
	if n == nil || n.fcm == nil || !n.fcm.Enabled() || device == nil || ev == nil {
		return false
	}
	if ev.EventType != domain.EventFireDetected || ev.Status != domain.EventActive {
		return false
	}
	if domain.SeverityRank(ev.Severity) < domain.SeverityRank(n.minSeverity) {
		return false
	}

	tokens, err := n.repo.ListNotificationTokens(ctx, []domain.UserRole{domain.RoleAdmin, domain.RolePetugas})
	if err != nil {
		n.log.Warn("failed to load follow-up push tokens", "event_id", ev.ID, "error", err)
		return false
	}
	if len(tokens) == 0 {
		return false
	}

	values := make([]string, 0, len(tokens))
	for i := range tokens {
		values = append(values, tokens[i].Token)
	}

	name := device.Name
	if name == "" {
		name = device.DeviceID
	}
	title := "PENGINGAT: Kebakaran belum diakui"
	body := fmt.Sprintf("Kebakaran di %s (%s) belum diakui petugas setelah 2 menit. Segera periksa lokasi.", name, device.DeviceID)
	notificationType := "fire_reminder"
	if escalation {
		title = "ESKALASI DARURAT: Kebakaran belum ditangani"
		body = fmt.Sprintf("Kebakaran di %s (%s) belum ditangani selama 5 menit. Segera lakukan penanganan.", name, device.DeviceID)
		notificationType = "fire_escalation"
	}

	data := map[string]string{
		"event_id":          ev.ID.String(),
		"device_id":         device.DeviceID,
		"device_name":       device.Name,
		"event_type":        string(ev.EventType),
		"severity":          string(ev.Severity),
		"triggered_at":      ev.TriggeredAt.Format("2006-01-02T15:04:05Z07:00"),
		"notification_type": notificationType,
	}

	invalid, sent, sendErr := n.fcm.Send(ctx, values, title, body, data)
	if sendErr != nil {
		n.log.Warn("fire follow-up FCM partially failed", "event_id", ev.ID, "notification_type", notificationType, "sent", sent, "error", sendErr)
		observability.NotificationsSent.WithLabelValues("error").Inc()
	}
	if sent > 0 {
		observability.NotificationsSent.WithLabelValues("sent").Inc()
		n.log.Info("fire follow-up notifications sent", "event_id", ev.ID, "notification_type", notificationType, "recipient_count", sent)
	}
	if len(invalid) > 0 {
		if err := n.repo.DeleteInvalidPushTokens(ctx, invalid); err != nil {
			n.log.Warn("failed to delete invalid push tokens", "error", err)
		}
	}
	return sent > 0
}

func notificationText(device *domain.Device, ev *domain.Event) (string, string) {
	name := device.Name
	if name == "" {
		name = device.DeviceID
	}
	switch ev.EventType {
	case domain.EventFireDetected:
		return "DARURAT: Kebakaran terdeteksi", fmt.Sprintf("Api terdeteksi di %s (%s). Pompa air diaktifkan otomatis.", name, device.DeviceID)
	case domain.EventSmokeDetected:
		return "Peringatan: Asap terdeteksi", fmt.Sprintf("Kadar asap/gas tinggi di %s (%s).", name, device.DeviceID)
	case domain.EventSoilDry:
		return "Peringatan: Tanah kering", fmt.Sprintf("Kelembapan tanah rendah di %s (%s).", name, device.DeviceID)
	case domain.EventTemperatureHigh:
		return "Peringatan: Suhu tinggi", fmt.Sprintf("Suhu lingkungan tinggi di %s (%s).", name, device.DeviceID)
	case domain.EventSensorError:
		return "Error sensor", fmt.Sprintf("Sensor bermasalah pada %s (%s): %s", name, device.DeviceID, ev.Message)
	case domain.EventDeviceOffline:
		return "Perangkat offline", fmt.Sprintf("%s (%s) tidak mengirim data.", name, device.DeviceID)
	default:
		return "Peringatan Fire Free Forest", fmt.Sprintf("%s: %s", name, ev.Message)
	}
}
