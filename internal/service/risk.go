package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/realtime"
	"firefreeforest/backend/internal/repository"
)

const (
	RiskNormal   = "NORMAL"
	RiskWaspada  = "WASPADA"
	RiskBahaya   = "BAHAYA"
	RiskDarurat  = "DARURAT"
	latestTTL    = 48 * time.Hour
	riskStateTTL = time.Hour
)

type LatestState struct {
	Environment *domain.TelemetryEnvironment `json:"environment,omitempty"`
	Fire        *domain.TelemetryFire        `json:"fire,omitempty"`
}

type Thresholds struct {
	TemperatureHighC float64
	SoilDryADC       float64
	SmokeLevelMax    int
}

type ConditionResult struct {
	Type     domain.EventType
	Severity domain.Severity
	Source   string
	Message  string
	Active   bool
}

type EvaluationResult struct {
	Events    []*domain.Event
	Commands  []*domain.Command
	RiskLevel string
}

type RiskEngine struct {
	repo     *repository.Repository
	rdb      *goredis.Client
	commands *CommandService
	cfg      *config.Config
	log      *slog.Logger
}

func NewRiskEngine(repo *repository.Repository, rdb *goredis.Client, commands *CommandService, cfg *config.Config, log *slog.Logger) *RiskEngine {
	return &RiskEngine{repo: repo, rdb: rdb, commands: commands, cfg: cfg, log: log}
}

func (e *RiskEngine) Thresholds() Thresholds {
	return Thresholds{
		TemperatureHighC: e.cfg.TemperatureHighC,
		SoilDryADC:       e.cfg.SoilDryADC,
		SmokeLevelMax:    e.cfg.SmokeLevelMax,
	}
}

func EvaluateConditions(state LatestState, th Thresholds) ([]ConditionResult, string) {
	results := make([]ConditionResult, 0, 4)

	if env := state.Environment; env != nil {
		temperatureHigh := env.Temperature != nil && *env.Temperature >= th.TemperatureHighC
		results = append(results, ConditionResult{
			Type:     domain.EventTemperatureHigh,
			Severity: domain.SeverityWarning,
			Source:   "dht11",
			Message:  "Suhu lingkungan melebihi ambang batas.",
			Active:   temperatureHigh,
		})
		soilDry := th.SoilDryADC > 0 && env.SoilMoisture < th.SoilDryADC
		results = append(results, ConditionResult{
			Type:     domain.EventSoilDry,
			Severity: domain.SeverityWarning,
			Source:   "soil_sensor",
			Message:  "Nilai kelembapan tanah berada di bawah ambang kalibrasi.",
			Active:   soilDry,
		})
	}

	if fire := state.Fire; fire != nil {
		results = append(results, ConditionResult{
			Type:     domain.EventFireDetected,
			Severity: domain.SeverityCritical,
			Source:   "flame_sensor",
			Message:  "Potensi kebakaran terdeteksi: api terbaca oleh flame sensor.",
			Active:   fire.FlameDetected,
		})
		results = append(results, ConditionResult{
			Type:     domain.EventSmokeDetected,
			Severity: domain.SeverityWarning,
			Source:   "mq2_sensor",
			Message:  "Kadar asap/gas melebihi ambang batas.",
			Active:   fire.SmokeLevel >= th.SmokeLevelMax,
		})
	}

	level := RiskNormal
	critical := false
	warnings := 0
	for _, r := range results {
		if !r.Active {
			continue
		}
		switch r.Severity {
		case domain.SeverityCritical:
			critical = true
		case domain.SeverityWarning:
			warnings++
		}
	}
	switch {
	case critical:
		level = RiskDarurat
	case warnings >= 2:
		level = RiskBahaya
	case warnings == 1:
		level = RiskWaspada
	}
	return results, level
}

func (e *RiskEngine) RecordEnvironment(ctx context.Context, device *domain.Device, tel *domain.TelemetryEnvironment) (*EvaluationResult, error) {
	if err := e.storeLatest(ctx, device.DeviceID, "environment", tel); err != nil {
		e.log.Warn("failed to cache environment telemetry", "device_id", device.DeviceID, "error", err)
	}
	state, err := e.loadLatest(ctx, device.DeviceID)
	if err != nil {
		return nil, err
	}
	return e.evaluateAndSync(ctx, device, state)
}

func (e *RiskEngine) RecordFire(ctx context.Context, device *domain.Device, tel *domain.TelemetryFire) (*EvaluationResult, error) {
	if err := e.storeLatest(ctx, device.DeviceID, "fire", tel); err != nil {
		e.log.Warn("failed to cache fire telemetry", "device_id", device.DeviceID, "error", err)
	}
	state, err := e.loadLatest(ctx, device.DeviceID)
	if err != nil {
		return nil, err
	}
	res, err := e.evaluateAndSync(ctx, device, state)
	if err != nil {
		return nil, err
	}
	if !tel.Pump && (tel.FlameDetected || tel.SmokeLevel >= e.cfg.SmokeLevelMax) {
		if cmd := e.maybeAutoPump(ctx, device); cmd != nil {
			res.Commands = append(res.Commands, cmd)
		}
	}
	return res, nil
}

func (e *RiskEngine) evaluateAndSync(ctx context.Context, device *domain.Device, state LatestState) (*EvaluationResult, error) {
	conditions, level := EvaluateConditions(state, e.Thresholds())
	res := &EvaluationResult{RiskLevel: level}

	activeRows, err := e.repo.ListActiveEventsForDevice(ctx, device.ID)
	if err != nil {
		return nil, err
	}
	activeByType := make(map[domain.EventType]*domain.Event, len(activeRows))
	for i := range activeRows {
		activeByType[activeRows[i].EventType] = &activeRows[i]
	}

	for _, cond := range conditions {
		existing := activeByType[cond.Type]
		if cond.Active {
			if existing != nil {
				continue
			}
			if !e.acquireCooldown(ctx, device.DeviceID, cond.Type) {
				continue
			}
			ev, err := e.createEvent(ctx, device, cond.Type, cond.Source, cond.Message, state, nil)
			if err != nil {
				e.releaseCooldown(ctx, device.DeviceID, cond.Type)
				return nil, err
			}
			res.Events = append(res.Events, ev)
			continue
		}

		if existing != nil {
			ok, err := e.repo.ResolveActiveEventByType(ctx, device.ID, cond.Type)
			if err != nil {
				e.log.Warn("failed to resolve event", "device_id", device.DeviceID, "event_type", cond.Type, "error", err)
				continue
			}
			if ok {
				now := time.Now().UTC()
				existing.Status = domain.EventResolved
				existing.ResolvedAt = &now
				existing.Device = device
				_ = realtime.Publish(ctx, e.rdb, realtime.Envelope{
					Type:     "event.resolved",
					DeviceID: device.DeviceID,
					Data: map[string]any{
						"event":     existing,
						"device_id": device.DeviceID,
					},
					Timestamp: now,
				})
			}
		}
	}

	e.setRiskLevel(ctx, device.DeviceID, level)
	return res, nil
}

func (e *RiskEngine) EmitSensorError(ctx context.Context, device *domain.Device, source, message string) (*domain.Event, error) {
	return e.emitEvent(ctx, device, domain.EventSensorError, source, message, e.cfg.EventCooldown, nil)
}

func (e *RiskEngine) EmitDeviceEvent(ctx context.Context, device *domain.Device, eventType domain.EventType, source, message string) (*domain.Event, error) {
	cooldown := e.cfg.EventCooldown
	if eventType == domain.EventFireDetected {
		cooldown = e.cfg.FireCooldown
	}
	return e.emitEvent(ctx, device, eventType, source, message, cooldown, nil)
}

func (e *RiskEngine) emitEvent(ctx context.Context, device *domain.Device, eventType domain.EventType, source, message string, cooldown time.Duration, meta map[string]any) (*domain.Event, error) {
	if !e.acquireCooldownFor(ctx, device.DeviceID, eventType, cooldown) {
		return nil, nil
	}
	ev, err := e.createEvent(ctx, device, eventType, source, message, LatestState{}, meta)
	if err != nil {
		e.releaseCooldown(ctx, device.DeviceID, eventType)
		return nil, err
	}
	return ev, nil
}

func (e *RiskEngine) createEvent(ctx context.Context, device *domain.Device, eventType domain.EventType, source, message string, state LatestState, extra map[string]any) (*domain.Event, error) {
	now := time.Now().UTC()
	meta := stateMetadata(state)
	for k, v := range extra {
		meta[k] = v
	}
	rawMeta, _ := json.Marshal(meta)

	ev := &domain.Event{
		ID:          uuid.New(),
		DeviceID:    device.ID,
		EventType:   eventType,
		Severity:    eventType.Severity(),
		Source:      source,
		Message:     message,
		Status:      domain.EventActive,
		Metadata:    rawMeta,
		TriggeredAt: now,
		CreatedAt:   now,
	}

	mqttPayload, err := json.Marshal(map[string]any{
		"device_id":  device.DeviceID,
		"event_id":   ev.ID.String(),
		"event_type": ev.EventType,
		"severity":   ev.Severity,
		"source":     ev.Source,
		"message":    ev.Message,
		"status":     ev.Status,
		"timestamp":  ev.TriggeredAt,
		"risk_level": "",
	})
	if err != nil {
		return nil, err
	}

	ob := &domain.Outbox{
		ID:            uuid.New(),
		Topic:         fmt.Sprintf("%s/%s/event", e.cfg.MQTTTopicPrefix, device.DeviceID),
		Payload:       mqttPayload,
		QoS:           1,
		AggregateType: "event",
		AggregateID:   ev.ID.String(),
		Status:        "pending",
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	if err := e.repo.CreateEventWithOutbox(ctx, ev, ob); err != nil {
		return nil, err
	}

	observability.RiskEvents.WithLabelValues(string(ev.EventType), string(ev.Severity)).Inc()
	ev.Device = device
	_ = realtime.Publish(ctx, e.rdb, realtime.Envelope{
		Type:     "event.created",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"event":     ev,
			"device_id": device.DeviceID,
		},
		Timestamp: now,
	})
	return ev, nil
}

func (e *RiskEngine) ResolveDeviceEvent(ctx context.Context, device *domain.Device, eventType domain.EventType) {
	rows, err := e.repo.ListActiveEventsForDevice(ctx, device.ID)
	if err != nil {
		e.log.Warn("failed to list active events", "device_id", device.DeviceID, "error", err)
		return
	}
	var target *domain.Event
	for i := range rows {
		if rows[i].EventType == eventType {
			target = &rows[i]
			break
		}
	}
	if target == nil {
		return
	}
	ok, err := e.repo.ResolveActiveEventByType(ctx, device.ID, eventType)
	if err != nil {
		e.log.Warn("failed to resolve event", "device_id", device.DeviceID, "event_type", eventType, "error", err)
		return
	}
	if !ok {
		return
	}
	now := time.Now().UTC()
	target.Status = domain.EventResolved
	target.ResolvedAt = &now
	target.Device = device
	_ = realtime.Publish(ctx, e.rdb, realtime.Envelope{
		Type:     "event.resolved",
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"event":     target,
			"device_id": device.DeviceID,
		},
		Timestamp: now,
	})
}

func (e *RiskEngine) maybeAutoPump(ctx context.Context, device *domain.Device) *domain.Command {
	if !e.cfg.AutoPumpEnabled {
		return nil
	}
	key := "ff:autopump:" + device.DeviceID
	ok, err := e.rdb.SetNX(ctx, key, "1", e.cfg.AutoPumpCooldown).Result()
	if err != nil {
		e.log.Warn("auto pump cooldown check failed", "device_id", device.DeviceID, "error", err)
		return nil
	}
	if !ok {
		return nil
	}

	duration := int(e.cfg.AutoPumpDuration.Seconds())
	cmd, err := e.commands.Issue(ctx, device, IssueCommandRequest{
		Action:  domain.ActionSetPump,
		Payload: map[string]any{"state": true, "duration": duration},
		Origin:  domain.OriginSystem,
	})
	if err != nil {
		_ = e.rdb.Del(ctx, key).Err()
		e.log.Error("failed to issue auto pump command", "device_id", device.DeviceID, "error", err)
		return nil
	}
	e.log.Info("auto pump command issued", "device_id", device.DeviceID, "command_id", cmd.CommandID, "duration", duration)
	return cmd
}

func (e *RiskEngine) storeLatest(ctx context.Context, deviceID, field string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	key := "ff:latest:" + deviceID
	pipe := e.rdb.TxPipeline()
	pipe.HSet(ctx, key, field, raw)
	pipe.Expire(ctx, key, latestTTL)
	_, err = pipe.Exec(ctx)
	return err
}

func (e *RiskEngine) loadLatest(ctx context.Context, deviceID string) (LatestState, error) {
	var state LatestState
	vals, err := e.rdb.HGetAll(ctx, "ff:latest:"+deviceID).Result()
	if err != nil && err != goredis.Nil {
		return state, err
	}
	if raw, ok := vals["environment"]; ok && raw != "" {
		var env domain.TelemetryEnvironment
		if err := json.Unmarshal([]byte(raw), &env); err == nil {
			state.Environment = &env
		}
	}
	if raw, ok := vals["fire"]; ok && raw != "" {
		var fire domain.TelemetryFire
		if err := json.Unmarshal([]byte(raw), &fire); err == nil {
			state.Fire = &fire
		}
	}
	return state, nil
}

func (e *RiskEngine) setRiskLevel(ctx context.Context, deviceID, level string) {
	key := "ff:risk:" + deviceID
	old, err := e.rdb.Get(ctx, key).Result()
	if err != nil && err != goredis.Nil {
		e.log.Warn("failed to read risk state", "device_id", deviceID, "error", err)
	}
	e.rdb.Set(ctx, key, level, riskStateTTL)
	if old != level {
		_ = realtime.Publish(ctx, e.rdb, realtime.Envelope{
			Type:     "risk.updated",
			DeviceID: deviceID,
			Data:     map[string]any{"level": level},
		})
	}
}

func (e *RiskEngine) acquireCooldown(ctx context.Context, deviceID string, eventType domain.EventType) bool {
	ttl := e.cfg.EventCooldown
	if eventType == domain.EventFireDetected {
		ttl = e.cfg.FireCooldown
	}
	return e.acquireCooldownFor(ctx, deviceID, eventType, ttl)
}

func (e *RiskEngine) acquireCooldownFor(ctx context.Context, deviceID string, eventType domain.EventType, ttl time.Duration) bool {
	ok, err := e.rdb.SetNX(ctx, cooldownKey(deviceID, eventType), "1", ttl).Result()
	if err != nil {
		e.log.Warn("cooldown check failed", "device_id", deviceID, "event_type", eventType, "error", err)
		return false
	}
	return ok
}

func (e *RiskEngine) releaseCooldown(ctx context.Context, deviceID string, eventType domain.EventType) {
	_ = e.rdb.Del(ctx, cooldownKey(deviceID, eventType)).Err()
}

func cooldownKey(deviceID string, eventType domain.EventType) string {
	return fmt.Sprintf("ff:cd:%s:%s", deviceID, eventType)
}

func stateMetadata(state LatestState) map[string]any {
	meta := map[string]any{}
	if env := state.Environment; env != nil {
		if env.Temperature != nil {
			meta["temperature"] = *env.Temperature
		}
		if env.Humidity != nil {
			meta["humidity"] = *env.Humidity
		}
		meta["soil_moisture"] = env.SoilMoisture
	}
	if fire := state.Fire; fire != nil {
		meta["flame_detected"] = fire.FlameDetected
		if fire.FlameLevel != nil {
			meta["flame_level"] = *fire.FlameLevel
		}
		meta["smoke_level"] = fire.SmokeLevel
		meta["pump"] = fire.Pump
	}
	return meta
}
