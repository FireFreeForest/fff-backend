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

// commandAckKeyPrefix is used to fan out command acknowledgements to goroutines
// waiting on a device response (e.g. the telemetry refresh before an event is
// created). It must match the key the risk engine polls.
const (
	commandAckKeyPrefix = "ff:cmdack:"
	commandAckTTL       = 5 * time.Minute
)

// maxADCReading is the highest value a 10-bit ADC can report. Some boards
// report 1024 (full scale) instead of 1023, so accept up to 1024.
const maxADCReading = 1024

func adcInRange(v float64) bool {
	return v >= 0 && v <= maxADCReading
}

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

type deviceTelemetryPayload struct {
	DeviceID        string `json:"device_id"`
	FirmwareVersion string `json:"firmware_version"`
	Wifi            struct {
		RSSI *int `json:"rssi"`
	} `json:"wifi"`
	Battery struct {
		Voltage    *float64 `json:"voltage"`
		Percentage *int     `json:"percentage"`
	} `json:"battery"`
	Timestamp string `json:"timestamp"`
}

type commandAckPayload struct {
	DeviceID  string `json:"device_id"`
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// deviceTelemetryIssue describes why a device telemetry payload was rejected
// before persistence. Source maps to the SENSOR_ERROR metadata reason.
type deviceTelemetryIssue struct {
	Source  string
	Message string
}

// validateDeviceTelemetry enforces the device telemetry contract: the payload
// must belong to the topic it arrived on, and WiFi/battery values must be
// physically plausible. It returns nil when the payload is acceptable.
func validateDeviceTelemetry(topicDeviceID string, p deviceTelemetryPayload) *deviceTelemetryIssue {
	if p.DeviceID != "" && p.DeviceID != topicDeviceID {
		return &deviceTelemetryIssue{
			Source:  "device_id_mismatch",
			Message: "device_id pada payload telemetri perangkat tidak sesuai dengan topik MQTT.",
		}
	}
	if p.Wifi.RSSI != nil && (*p.Wifi.RSSI < -120 || *p.Wifi.RSSI > 0) {
		return &deviceTelemetryIssue{
			Source:  "value_out_of_range",
			Message: fmt.Sprintf("RSSI WiFi tidak valid: %d", *p.Wifi.RSSI),
		}
	}
	if p.Battery.Percentage != nil && (*p.Battery.Percentage < 0 || *p.Battery.Percentage > 100) {
		return &deviceTelemetryIssue{
			Source:  "value_out_of_range",
			Message: fmt.Sprintf("Persentase baterai tidak valid: %d", *p.Battery.Percentage),
		}
	}
	if p.Battery.Voltage != nil && (*p.Battery.Voltage < 0 || *p.Battery.Voltage > 6) {
		return &deviceTelemetryIssue{
			Source:  "value_out_of_range",
			Message: fmt.Sprintf("Tegangan baterai tidak valid: %.3f", *p.Battery.Voltage),
		}
	}
	return nil
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

func (w *Worker) handleDeviceTelemetry(_ paho.Client, msg paho.Message) {
	w.process("device", msg, w.processDeviceTelemetry)
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
	if !adcInRange(*p.Sensors.SoilMoisture) {
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
	if !adcInRange(float64(*p.Sensors.SmokeLevel)) {
		w.emitSensorError(ctx, device, "value_out_of_range", fmt.Sprintf("Kadar asap tidak valid: %d", *p.Sensors.SmokeLevel))
		return nil
	}
	if p.Sensors.FlameLevel != nil && !adcInRange(float64(*p.Sensors.FlameLevel)) {
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

func (w *Worker) processDeviceTelemetry(ctx context.Context, topicDeviceID string, payload []byte) error {
	var p deviceTelemetryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		device, derr := w.resolveDevice(ctx, topicDeviceID)
		if derr == nil {
			w.emitSensorError(ctx, device, "payload_decode", "Payload telemetri perangkat bukan JSON yang valid.")
		}
		return nil
	}

	device, err := w.resolveDevice(ctx, topicDeviceID)
	if err != nil {
		return err
	}
	if issue := validateDeviceTelemetry(topicDeviceID, p); issue != nil {
		w.emitSensorError(ctx, device, issue.Source, issue.Message)
		return nil
	}

	now := time.Now().UTC()
	firmware := strings.TrimSpace(p.FirmwareVersion)
	tel := &domain.TelemetryDevice{
		DeviceID:          device.ID,
		FirmwareVersion:   firmware,
		WifiRSSI:          p.Wifi.RSSI,
		BatteryVoltage:    p.Battery.Voltage,
		BatteryPercentage: p.Battery.Percentage,
		RecordedAt:        parseRecordedAt(p.Timestamp),
		ReceivedAt:        now,
	}
	if err := w.repo.CreateDeviceTelemetry(ctx, tel); err != nil {
		return err
	}
	if firmware != "" && firmware != device.FirmwareVersion {
		if err := w.repo.UpdateDeviceFields(ctx, device.ID, map[string]any{"firmware_version": firmware}); err != nil {
			w.log.Warn("failed to update device firmware version", "device_id", device.DeviceID, "error", err)
		} else {
			device.FirmwareVersion = firmware
		}
	}
	res, err := w.risk.RecordDevice(ctx, device, tel)
	if err != nil {
		w.log.Error("risk evaluation failed", "device_id", device.DeviceID, "error", err)
	} else {
		for _, ev := range res.Events {
			w.log.Info("dispatching event to notifier",
				"device_id", device.DeviceID,
				"event_type", ev.EventType,
				"severity", ev.Severity,
			)
			w.notifier.NotifyEvent(ctx, device, ev)
		}
	}
	_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
		Type:     "telemetry.device",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"telemetry": tel,
			"device_id": device.DeviceID,
		},
		Timestamp: now,
	})
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

	// Signal any waiter (e.g. the telemetry refresh used before event creation)
	// as soon as the device confirms, independent of the command row update.
	if err := w.rdb.Set(ctx, commandAckKeyPrefix+p.CommandID, string(status), commandAckTTL).Err(); err != nil {
		w.log.Warn("failed to signal command ack", "command_id", p.CommandID, "error", err)
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
			ID:          uuid.New(),
			DeviceID:    deviceID,
			Name:        deviceID,
			Status:      domain.DeviceOnline,
			LastSeenAt:  &now,
			OnlineSince: &now,
			CreatedAt:   now,
			UpdatedAt:   now,
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
				Data: map[string]any{
					"status":         string(domain.DeviceOnline),
					"online":         true,
					"online_since":   now,
					"uptime_seconds": int64(0),
				},
			})
			return ensured, nil
		}
		device = ensured
	}

	wasOnline := device.Status == domain.DeviceOnline
	now := time.Now().UTC()
	onlineSince := now
	if wasOnline && device.OnlineSince != nil {
		onlineSince = *device.OnlineSince
	}
	if err := w.repo.TouchDevice(ctx, deviceID, now); err != nil {
		return nil, err
	}
	device.Status = domain.DeviceOnline
	device.LastSeenAt = &now
	device.OnlineSince = &onlineSince
	if !wasOnline {
		w.risk.ResolveDeviceEvent(ctx, device, domain.EventDeviceOffline)
		_ = realtime.Publish(ctx, w.rdb, realtime.Envelope{
			Type:     "device.status",
			DeviceID: deviceID,
			Data: map[string]any{
				"status":         string(domain.DeviceOnline),
				"online":         true,
				"last_seen_at":   now,
				"online_since":   now,
				"uptime_seconds": int64(0),
			},
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
