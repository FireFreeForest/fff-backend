package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/platform/fcm"
	"firefreeforest/backend/internal/repository"
)

type Notifier struct {
	fcm          *fcm.Client
	repo         *repository.Repository
	log          *slog.Logger
	minSeverity  domain.Severity
	allowedTypes map[domain.EventType]struct{}
}

func NewNotifier(fcmClient *fcm.Client, repo *repository.Repository, minSeverity domain.Severity, eventTypes []string, log *slog.Logger) *Notifier {
	allowed := make(map[domain.EventType]struct{}, len(eventTypes))
	for _, raw := range eventTypes {
		eventType := domain.EventType(strings.ToUpper(strings.TrimSpace(raw)))
		if domain.ValidEventType(eventType) {
			allowed[eventType] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	return &Notifier{fcm: fcmClient, repo: repo, log: log, minSeverity: minSeverity, allowedTypes: allowed}
}

func (n *Notifier) allowsEvent(eventType domain.EventType) bool {
	if len(n.allowedTypes) == 0 {
		return true
	}
	_, ok := n.allowedTypes[eventType]
	return ok
}

func (n *Notifier) NotifyEvent(ctx context.Context, device *domain.Device, ev *domain.Event) {
	if n == nil || ev == nil {
		return
	}
	if n.fcm == nil || !n.fcm.Enabled() {
		n.recordNotification(ctx, ev, domain.TimelineNotification, domain.TimelineOutcomeSkipped, "fcm_disabled",
			"Peringatan tidak dikirim karena FCM tidak aktif.", nil)
		return
	}
	if device == nil {
		return
	}
	if domain.SeverityRank(ev.Severity) < domain.SeverityRank(n.minSeverity) {
		n.recordNotification(ctx, ev, domain.TimelineNotification, domain.TimelineOutcomeSkipped, "severity_below_min",
			"Peringatan dilewati karena severity di bawah ambang notifikasi.", nil)
		return
	}
	if !n.allowsEvent(ev.EventType) {
		n.recordNotification(ctx, ev, domain.TimelineNotification, domain.TimelineOutcomeSkipped, "event_type_filtered",
			"Peringatan dilewati karena tipe event tidak diaktifkan.", nil)
		return
	}

	tokens, err := n.repo.ListNotificationTokens(ctx, []domain.UserRole{domain.RoleAdmin, domain.RolePetugas})
	if err != nil {
		n.log.Warn("failed to load push tokens", "error", err)
		n.recordNotification(ctx, ev, domain.TimelineNotification, domain.TimelineOutcomeFailed, "load_tokens_failed",
			"Peringatan gagal disiapkan karena token penerima tidak dapat dimuat.", map[string]any{"error": timelineError(err)})
		return
	}
	if len(tokens) == 0 {
		n.recordNotification(ctx, ev, domain.TimelineNotification, domain.TimelineOutcomeSkipped, "no_recipients",
			"Peringatan dilewati karena tidak ada token penerima.", nil)
		return
	}
	values := make([]string, 0, len(tokens))
	for i := range tokens {
		values = append(values, tokens[i].Token)
	}

	title, body := notificationText(device, ev)
	channelID, sound := notificationChannel(ev.EventType)
	data := map[string]string{
		"event_id":     ev.ID.String(),
		"device_id":    device.DeviceID,
		"device_name":  device.Name,
		"event_type":   string(ev.EventType),
		"severity":     string(ev.Severity),
		"triggered_at": ev.TriggeredAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	invalid, sent, err := n.fcm.Send(ctx, values, title, body, channelID, sound, data)
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

	outcome, reason := notificationOutcome(sent, err)
	meta := map[string]any{
		"notification_type": "event",
		"channel":           channelID,
		"recipient_count":   len(values),
		"sent_count":        sent,
		"invalid_count":     len(invalid),
	}
	if err != nil {
		meta["error"] = timelineError(err)
	}
	n.recordNotification(ctx, ev, domain.TimelineNotification, outcome, reason,
		notificationSentMessage(outcome, sent, len(values)), meta)
}

// NotifyFireFollowUp sends a reminder or escalation for an unacknowledged fire event.
func (n *Notifier) NotifyFireFollowUp(ctx context.Context, device *domain.Device, ev *domain.Event, escalation bool) bool {
	kind := domain.TimelineReminder
	notificationType := "fire_reminder"
	if escalation {
		kind = domain.TimelineEscalation
		notificationType = "fire_escalation"
	}
	if ev == nil {
		return false
	}
	if n == nil || n.fcm == nil || !n.fcm.Enabled() {
		n.recordNotification(ctx, ev, kind, domain.TimelineOutcomeSkipped, "fcm_disabled",
			"Notifikasi tidak dikirim karena FCM tidak aktif.", map[string]any{"notification_type": notificationType})
		return false
	}
	if device == nil {
		return false
	}
	if ev.EventType != domain.EventFireDetected || ev.Status != domain.EventActive {
		return false
	}
	if domain.SeverityRank(ev.Severity) < domain.SeverityRank(n.minSeverity) {
		n.recordNotification(ctx, ev, kind, domain.TimelineOutcomeSkipped, "severity_below_min",
			"Notifikasi dilewati karena severity di bawah ambang.", map[string]any{"notification_type": notificationType})
		return false
	}
	if !n.allowsEvent(ev.EventType) {
		n.recordNotification(ctx, ev, kind, domain.TimelineOutcomeSkipped, "event_type_filtered",
			"Notifikasi dilewati karena tipe event tidak diaktifkan.", map[string]any{"notification_type": notificationType})
		return false
	}

	tokens, err := n.repo.ListNotificationTokens(ctx, []domain.UserRole{domain.RoleAdmin, domain.RolePetugas})
	if err != nil {
		n.log.Warn("failed to load follow-up push tokens", "event_id", ev.ID, "error", err)
		n.recordNotification(ctx, ev, kind, domain.TimelineOutcomeFailed, "load_tokens_failed",
			"Notifikasi gagal disiapkan karena token penerima tidak dapat dimuat.",
			map[string]any{"notification_type": notificationType, "error": timelineError(err)})
		return false
	}
	if len(tokens) == 0 {
		n.recordNotification(ctx, ev, kind, domain.TimelineOutcomeSkipped, "no_recipients",
			"Notifikasi dilewati karena tidak ada token penerima.", map[string]any{"notification_type": notificationType})
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
	if escalation {
		title = "ESKALASI DARURAT: Kebakaran belum ditangani"
		body = fmt.Sprintf("Kebakaran di %s (%s) belum ditangani selama 5 menit. Segera lakukan penanganan.", name, device.DeviceID)
	}

	channelID, sound := notificationChannel(ev.EventType)
	data := map[string]string{
		"event_id":          ev.ID.String(),
		"device_id":         device.DeviceID,
		"device_name":       device.Name,
		"event_type":        string(ev.EventType),
		"severity":          string(ev.Severity),
		"triggered_at":      ev.TriggeredAt.Format("2006-01-02T15:04:05Z07:00"),
		"notification_type": notificationType,
	}

	invalid, sent, sendErr := n.fcm.Send(ctx, values, title, body, channelID, sound, data)
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

	outcome, reason := notificationOutcome(sent, sendErr)
	meta := map[string]any{
		"notification_type": notificationType,
		"channel":           channelID,
		"recipient_count":   len(values),
		"sent_count":        sent,
		"invalid_count":     len(invalid),
	}
	if sendErr != nil {
		meta["error"] = timelineError(sendErr)
	}
	n.recordNotification(ctx, ev, kind, outcome, reason,
		notificationSentMessage(outcome, sent, len(values)), meta)
	return sent > 0
}

func (n *Notifier) recordNotification(
	ctx context.Context,
	ev *domain.Event,
	kind domain.EventTimelineKind,
	outcome domain.EventTimelineOutcome,
	reason string,
	message string,
	meta map[string]any,
) {
	if n == nil || n.repo == nil || ev == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["reason"] = reason
	var dedupe *string
	if outcome == domain.TimelineOutcomeSkipped {
		key := fmt.Sprintf("%s:%s:%s", ev.ID, kind, reason)
		dedupe = &key
	}
	entry := &domain.EventTimeline{
		EventID:   ev.ID,
		Kind:      kind,
		Outcome:   &outcome,
		ActorType: domain.TimelineActorSystem,
		ActorName: "Sistem",
		Title:     domain.NotificationTimelineTitle(kind, outcome),
		Message:   message,
		Level:     domain.NotificationTimelineLevel(outcome),
		Metadata:  domain.TimelineMetadata(meta),
		DedupeKey: dedupe,
	}
	if err := n.repo.CreateEventTimeline(ctx, entry); err != nil {
		n.log.Warn("failed to write event timeline notification", "event_id", ev.ID, "kind", kind, "error", err)
	}
}

func notificationOutcome(sent int, err error) (domain.EventTimelineOutcome, string) {
	if err != nil {
		if sent > 0 {
			return domain.TimelineOutcomePartial, "partial_delivery"
		}
		return domain.TimelineOutcomeFailed, "send_failed"
	}
	if sent > 0 {
		return domain.TimelineOutcomeSent, "delivered"
	}
	return domain.TimelineOutcomeFailed, "no_delivery"
}

func notificationSentMessage(outcome domain.EventTimelineOutcome, sent, total int) string {
	switch outcome {
	case domain.TimelineOutcomePartial:
		return fmt.Sprintf("Peringatan terkirim ke %d dari %d perangkat.", sent, total)
	case domain.TimelineOutcomeSent:
		return fmt.Sprintf("Peringatan berhasil dikirim ke %d perangkat.", sent)
	case domain.TimelineOutcomeSkipped:
		return "Peringatan tidak dikirim."
	default:
		return "Peringatan gagal dikirim."
	}
}

func timelineError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func notificationChannel(eventType domain.EventType) (string, string) {
	switch eventType {
	case domain.EventFireDetected:
		return "fire_alerts_fire", "notif_fire"
	case domain.EventSensorError, domain.EventDeviceOffline:
		return "fire_alerts_error", "notif_sensor_error"
	default:
		return "fire_alerts_warning", "notif_smoke"
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
