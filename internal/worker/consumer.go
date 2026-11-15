package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/realtime"
	"firefreeforest/backend/internal/repository"
)

const maxPayloadBytes = 64 * 1024

type environmentPayload struct {
	DeviceID string `json:"device_id"`
	Sensors  struct {
		Temperature  *float64 `json:"temperature"`
		Humidity     *float64 `json:"humidity"`
		SoilMoisture *float64 `json:"soil_moisture"`
	} `json:"sensors"`
	Timestamp string `json:"timestamp"`
}

type firePayload struct {
	DeviceID string `json:"device_id"`
	Sensors  struct {
		FlameDetected *bool `json:"flame_detected"`
		FlameLevel    *int  `json:"flame_level"`
		SmokeLevel    *int  `json:"smoke_level"`
	} `json:"sensors"`
	Actuators struct {
		Pump   *bool `json:"pump"`
		Buzzer *bool `json:"buzzer"`
	} `json:"actuators"`
	Timestamp string `json:"timestamp"`
}

type deviceEventPayload struct {
	DeviceID  string `json:"device_id"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Severity  string `json:"severity"`
	Source    string `json:"source"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type commandAckPayload struct {
	DeviceID  string `json:"device_id"`
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type processor func(ctx context.Context, deviceID string, payload []byte) error

func (w *Worker) subscribe(topic string, qos byte, handler paho.MessageHandler) {
	if err := w.mqtt.Subscribe(topic, qos, handler); err != nil {
		w.log.Error("failed to subscribe", "topic", topic, "error", err)
	}
}

func (w *Worker) process(kind string, msg paho.Message, fn processor) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(w.ctx, 20*time.Second)
	defer cancel()

	status := "ok"
	deviceID, ok := topicDeviceID(msg.Topic(), w.cfg.MQTTTopicPrefix)
	if !ok {
		status = "rejected_topic"
		w.log.Warn("rejected mqtt topic", "topic", msg.Topic())
	} else if len(msg.Payload()) > maxPayloadBytes {
		status = "rejected_size"
		w.log.Warn("rejected mqtt payload", "topic", msg.Topic(), "bytes", len(msg.Payload()))
	} else if err := fn(ctx, deviceID, msg.Payload()); err != nil {
		status = "error"
		w.log.Error("failed to process mqtt message", "type", kind, "device_id", deviceID, "error", err)
	}

	observability.MQTTMessages.WithLabelValues(kind, status).Inc()
	observability.MQTTIngestDuration.WithLabelValues(kind).Observe(time.Since(start).Seconds())
}

func topicDeviceID(topic, prefix string) (string, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) < 3 || parts[0] != prefix {
		return "", false
	}
	deviceID := strings.TrimSpace(parts[1])
	if deviceID == "" || strings.ContainsAny(deviceID, "+#") {
		return "", false
	}
	return deviceID, true
}

func (w *Worker) handleEnvironment(_ paho.Client, msg paho.Message) {
	w.process("environment", msg, w.processEnvironment)
}

func (w *Worker) handleFire(_ paho.Client, msg paho.Message) {
	w.process("fire", msg, w.processFire)
}

func (w *Worker) handleDeviceEvent(_ paho.Client, msg paho.Message) {
	w.process("event", msg, w.processDeviceEvent)
}

func (w *Worker) handleCommandAck(_ paho.Client, msg paho.Message) {
	w.process("command_ack", msg, w.processCommandAck)
}

func (w *Worker) processEnvironment(ctx context.Context, topicDeviceID string, payload []byte) error {
	var p environmentPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		device, derr := w.resolveDevice(ctx, topicDeviceID)
		if derr == nil {
			w.emitSensorError(ctx, device, "payload_decode", "Payload telemetri lingkungan bukan JSON yang valid.")
		}
		return nil
	}

	device, err := w.resolveDevice(ctx, topicDeviceID)
	if err != nil {
		return err
	}
	if p.DeviceID != "" && p.DeviceID != topicDeviceID {
		w.emitSensorError(ctx, device, "device_id_mismatch", "device_id pada payload tidak sesuai dengan topik MQTT.")
		return nil
	}
	if p.Sensors.SoilMoisture == nil {
		w.emitSensorError(ctx, device, "payload_incomplete", "Data kelembapan tanah tidak tersedia.")
		return nil
	}
	if p.Sensors.Temperature != nil && (*p.Sensors.Temperature < -10 || *p.Sensors.Temperature > 80) {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Suhu tidak valid: %.2f", *p.Sensors.Temperature))
		return nil
	}
	if p.Sensors.Humidity != nil && (*p.Sensors.Humidity < 0 || *p.Sensors.Humidity > 100) {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Kelembapan udara tidak valid: %.2f", *p.Sensors.Humidity))
		return nil
	}
	if *p.Sensors.SoilMoisture < 0 || *p.Sensors.SoilMoisture > 1023 {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Nilai tanah tidak valid: %.2f", *p.Sensors.SoilMoisture))
		return nil
	}

	now := time.Now().UTC()
	tel := &domain.TelemetryEnvironment{
		DeviceID:     device.ID,
		Temperature:  p.Sensors.Temperature,
		Humidity:     p.Sensors.Humidity,
		SoilMoisture: *p.Sensors.SoilMoisture,
		RecordedAt:   parseRecordedAt(p.Timestamp),
		ReceivedAt:   now,
	}
	if err := w.repo.CreateEnvironment(ctx, tel); err != nil {
		return err
	}
	_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
		Type:     "telemetry.environment",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"telemetry": tel,
			"device_id": device.DeviceID,
		},
		Timestamp: now,
	})

	res, err := w.risk.RecordEnvironment(ctx, device, tel)
	if err != nil {
		w.log.Error("risk evaluation failed", "device_id", device.DeviceID, "error", err)
		return nil
	}
	for _, ev := range res.Events {
		w.notifier.NotifyEvent(ctx, device, ev)
	}
	return nil
}

func (w *Worker) processFire(ctx context.Context, topicDeviceID string, payload []byte) error {
	var p firePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		device, derr := w.resolveDevice(ctx, topicDeviceID)
		if derr == nil {
			w.emitSensorError(ctx, device, "payload_decode", "Payload telemetri kebakaran bukan JSON yang valid.")
		}
		return nil
	}

	device, err := w.resolveDevice(ctx, topicDeviceID)
	if err != nil {
		return err
	}
	if p.DeviceID != "" && p.DeviceID != topicDeviceID {
		w.emitSensorError(ctx, device, "device_id_mismatch", "device_id pada payload tidak sesuai dengan topik MQTT.")
		return nil
	}
	if p.Sensors.FlameDetected == nil || p.Sensors.SmokeLevel == nil {
		w.emitSensorError(ctx, device, "payload_incomplete", "Data sensor api/asap tidak lengkap.")
		return nil
	}
	if *p.Sensors.SmokeLevel < 0 || *p.Sensors.SmokeLevel > 1023 {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Kadar asap tidak valid: %d", *p.Sensors.SmokeLevel))
		return nil
	}
	if p.Sensors.FlameLevel != nil && (*p.Sensors.FlameLevel < 0 || *p.Sensors.FlameLevel > 1023) {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Nilai api tidak valid: %d", *p.Sensors.FlameLevel))
		return nil
	}

	pump := false
	buzzer := false
	if p.Actuators.Pump != nil {
		pump = *p.Actuators.Pump
	}
	if p.Actuators.Buzzer != nil {
		buzzer = *p.Actuators.Buzzer
	}

	now := time.Now().UTC()
	tel := &domain.TelemetryFire{
		DeviceID:      device.ID,
		FlameDetected: *p.Sensors.FlameDetected,
		FlameLevel:    p.Sensors.FlameLevel,
		SmokeLevel:    *p.Sensors.SmokeLevel,
		Pump:          pump,
		Buzzer:        buzzer,
		RecordedAt:    parseRecordedAt(p.Timestamp),
		ReceivedAt:    now,
	}
	if err := w.repo.CreateFire(ctx, tel); err != nil {
		return err
	}
	_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
		Type:     "telemetry.fire",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"telemetry": tel,
			"device_id": device.DeviceID,
		},
		Timestamp: now,
	})

	res, err := w.risk.RecordFire(ctx, device, tel)
	if err != nil {
		w.log.Error("risk evaluation failed", "device_id", device.DeviceID, "error", err)
		return nil
	}

	w.log.Info("fire risk evaluation completed",
		"device_id", device.DeviceID,
		"flame_detected", tel.FlameDetected,
		"event_count", len(res.Events),
	)

	for _, ev := range res.Events {
		w.log.Info("dispatching event to notifier",
			"device_id", device.DeviceID,
			"event_type", ev.EventType,
			"severity", ev.Severity,
		)
		w.notifier.NotifyEvent(ctx, device, ev)
	}
	return nil
}

func (w *Worker) processDeviceEvent(ctx context.Context, topicDeviceID string, payload []byte) error {
	var p deviceEventPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		device, derr := w.resolveDevice(ctx, topicDeviceID)
		if derr == nil {
			w.emitSensorError(ctx, device, "payload_decode", "Payload event bukan JSON yang valid.")
		}
		return nil
	}
	if p.EventID != "" {
		return nil
	}

	device, err := w.resolveDevice(ctx, topicDeviceID)
	if err != nil {
		return err
	}
	if p.DeviceID != "" && p.DeviceID != topicDeviceID {
		w.emitSensorError(ctx, device, "device_id_mismatch", "device_id pada payload event tidak sesuai dengan topik MQTT.")
		return nil
	}

	eventType := domain.EventType(strings.ToUpper(strings.TrimSpace(p.EventType)))
	if !domain.ValidEventType(eventType) {
		w.emitSensorError(ctx, device, "invalid_event_type", "Tipe event dari perangkat tidak dikenal: "+p.EventType)
		return nil
	}
	source := p.Source
	if source == "" {
		source = "device"
	}
	message := p.Message
	if message == "" {
		message = "Event dilaporkan oleh perangkat."
	}

	ev, err := w.risk.EmitDeviceEvent(ctx, device, eventType, source, message)
	if err != nil {
		return err
	}
	if ev != nil {
		w.notifier.NotifyEvent(ctx, device, ev)
	}
	return nil
}

func (w *Worker) processCommandAck(ctx context.Context, topicDeviceID string, payload []byte) error {
	var p commandAckPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	if strings.TrimSpace(p.CommandID) == "" {
		return nil
	}
	device, err := w.resolveDevice(ctx, topicDeviceID)
	if err != nil {
		return err
	}
	if p.DeviceID != "" && p.DeviceID != topicDeviceID {
		return nil
	}

	var status domain.CommandStatus
	switch strings.ToLower(strings.TrimSpace(p.Status)) {
	case "success":
		status = domain.CommandSuccess
	case "failed", "error":
		status = domain.CommandFailed
	default:
		w.log.Warn("unknown command ack status", "device_id", device.DeviceID, "status", p.Status)
		return nil
	}

	cmd, updated, err := w.repo.AckCommand(ctx, device.ID, p.CommandID, status, p.Message)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			w.log.Warn("command ack for unknown command", "device_id", device.DeviceID, "command_id", p.CommandID)
			return nil
		}
		return err
	}
	if !updated {
		return nil
	}
	_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
		Type:     "command.ack",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"command_id": cmd.CommandID,
			"status":     cmd.Status,
			"message":    cmd.AckMessage,
			"device_id":  device.DeviceID,
		},
	})
	return nil
}

func (w *Worker) resolveDevice(ctx context.Context, deviceID string) (*domain.Device, error) {
	device, err := w.repo.GetDeviceByDeviceID(ctx, deviceID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		if !w.cfg.AutoRegisterDevices {
			return nil, fmt.Errorf("unknown device %s", deviceID)
		}
		now := time.Now().UTC()
		candidate := &domain.Device{
			ID:         uuid.New(),
			DeviceID:   deviceID,
			Name:       deviceID,
			Status:     domain.DeviceOnline,
			LastSeenAt: &now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		created, ensured, err := w.repo.EnsureDevice(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if created {
			w.log.Info("auto-registered device", "device_id", deviceID)
			_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
				Type:     "device.status",
				DeviceID: deviceID,
				Data:     map[string]any{"status": string(domain.DeviceOnline), "online": true},
			})
			return ensured, nil
		}
		device = ensured
	}

	wasOnline := device.Status == domain.DeviceOnline
	now := time.Now().UTC()
	if err := w.repo.TouchDevice(ctx, deviceID, now); err != nil {
		return nil, err
	}
	device.Status = domain.DeviceOnline
	device.LastSeenAt = &now
	if !wasOnline {
		w.risk.ResolveDeviceEvent(ctx, device, domain.EventDeviceOffline)
		_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
			Type:     "device.status",
			DeviceID: deviceID,
			Data:     map[string]any{"status": string(domain.DeviceOnline), "online": true, "last_seen_at": now},
		})
	}
	return device, nil
}

func (w *Worker) emitSensorError(ctx context.Context, device *domain.Device, source, message string) {
	ev, err := w.risk.EmitSensorError(ctx, device, source, message)
	if err != nil {
		w.log.Error("failed to emit sensor error event", "device_id", device.DeviceID, "error", err)
		return
	}
	if ev != nil {
		w.notifier.NotifyEvent(ctx, device, ev)
	}
}

func parseRecordedAt(raw string) time.Time {
	now := time.Now().UTC()
	if strings.TrimSpace(raw) == "" {
		return now
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return now
	}
	t = t.UTC()
	if t.After(now.Add(5 * time.Minute)) {
		return now
	}
	return t
}
