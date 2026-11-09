package worker

import (
	"context"
	"time"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/realtime"
)

func (w *Worker) runSweeper(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	w.sweepCommands(ctx)
	w.sweepOfflineDevices(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweepCommands(ctx)
			w.sweepOfflineDevices(ctx)
		}
	}
}

func (w *Worker) sweepCommands(ctx context.Context) {
	cutoff := time.Now().Add(-w.cfg.CommandTimeout)
	rows, err := w.repo.MarkCommandTimeouts(ctx, cutoff)
	if err != nil {
		w.log.Error("failed to mark command timeouts", "error", err)
		return
	}
	for _, row := range rows {
		_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
			Type:     "command.timeout",
			DeviceID: row.DeviceID.String(),
			Data: map[string]any{
				"command_id": row.CommandID,
			},
		})
	}
	if len(rows) > 0 {
		w.log.Warn("commands timed out", "count", len(rows))
	}
}

func (w *Worker) sweepOfflineDevices(ctx context.Context) {
	cutoff := time.Now().Add(-w.cfg.OfflineAfter)
	deviceIDs, err := w.repo.MarkStaleDevicesOffline(ctx, cutoff)
	if err != nil {
		w.log.Error("failed to mark offline devices", "error", err)
		return
	}
	for _, deviceID := range deviceIDs {
		_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
			Type:     "device.status",
			DeviceID: deviceID,
			Data:     map[string]any{"status": string(domain.DeviceOffline), "online": false},
		})

		device, err := w.repo.GetDeviceByDeviceID(ctx, deviceID)
		if err != nil {
			continue
		}
		ev, err := w.risk.EmitDeviceEvent(ctx, device, domain.EventDeviceOffline, "heartbeat", "Perangkat tidak mengirim data melebihi batas waktu.")
		if err != nil {
			w.log.Warn("failed to emit device offline event", "device_id", deviceID, "error", err)
			continue
		}
		if ev != nil {
			w.notifier.NotifyEvent(ctx, device, ev)
		}
	}
}

func (w *Worker) runRetention(ctx context.Context) {
	if w.cfg.RetentionDays <= 0 {
		return
	}
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-time.Duration(w.cfg.RetentionDays) * 24 * time.Hour)
			n, err := w.repo.DeleteTelemetryBefore(ctx, cutoff)
			if err != nil {
				w.log.Error("telemetry retention failed", "error", err)
				continue
			}
			if n > 0 {
				w.log.Info("telemetry retention deleted rows", "rows", n, "cutoff", cutoff)
			}
			observability.MQTTMessages.WithLabelValues("retention", "ok").Inc()
		}
	}
}
