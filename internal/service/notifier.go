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

	invalid, err := n.fcm.Send(ctx, values, title, body, data)
	if err != nil {
		n.log.Warn("fcm send partially failed", "device_id", device.DeviceID, "event_type", ev.EventType, "error", err)
		observability.NotificationsSent.WithLabelValues("error").Inc()
	} else {
		observability.NotificationsSent.WithLabelValues("sent").Inc()
	}
	if len(invalid) > 0 {
		if err := n.repo.DeleteInvalidPushTokens(ctx, invalid); err != nil {
			n.log.Warn("failed to delete invalid push tokens", "error", err)
		}
	}
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
