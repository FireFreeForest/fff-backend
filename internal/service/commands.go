package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/realtime"
	"firefreeforest/backend/internal/repository"
)

type CommandService struct {
	repo        *repository.Repository
	rdb         *goredis.Client
	log         *slog.Logger
	maxDuration int
}

func NewCommandService(repo *repository.Repository, rdb *goredis.Client, log *slog.Logger, maxDuration time.Duration) *CommandService {
	seconds := int(maxDuration.Seconds())
	if seconds <= 0 {
		seconds = 300
	}
	return &CommandService{repo: repo, rdb: rdb, log: log, maxDuration: seconds}
}

type IssueCommandRequest struct {
	Action   domain.CommandAction
	Payload  map[string]any
	Origin   domain.CommandOrigin
	IssuedBy *uuid.UUID
}

func (s *CommandService) Issue(ctx context.Context, device *domain.Device, req IssueCommandRequest) (*domain.Command, error) {
	if req.Action != domain.ActionSetPump {
		return nil, fmt.Errorf("%w: unsupported action", ErrValidation)
	}
	state, ok := req.Payload["state"].(bool)
	if !ok {
		return nil, fmt.Errorf("%w: payload state is required", ErrValidation)
	}
	duration, _ := toInt(req.Payload["duration"])
	if state && (duration < 1 || duration > s.maxDuration) {
		return nil, fmt.Errorf("%w: duration must be between 1 and %d seconds", ErrValidation, s.maxDuration)
	}
	if !state {
		duration = 0
	}
	req.Payload["state"] = state
	req.Payload["duration"] = duration

	inner, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	for attempt := 0; attempt < 5; attempt++ {
		commandID := newCommandID(now)
		cmd := &domain.Command{
			ID:        uuid.New(),
			CommandID: commandID,
			DeviceID:  device.ID,
			Action:    req.Action,
			Payload:   inner,
			Status:    domain.CommandPending,
			Origin:    req.Origin,
			IssuedBy:  req.IssuedBy,
			IssuedAt:  now,
			CreatedAt: now,
			UpdatedAt: now,
		}
		mqttPayload, err := json.Marshal(map[string]any{
			"device_id":  device.DeviceID,
			"command_id": commandID,
			"action":     string(req.Action),
			"payload":    req.Payload,
		})
		if err != nil {
			return nil, err
		}
		ob := &domain.Outbox{
			ID:            uuid.New(),
			Topic:         fmt.Sprintf("free-flame-forest/%s/command", device.DeviceID),
			Payload:       mqttPayload,
			QoS:           1,
			AggregateType: "command",
			AggregateID:   cmd.ID.String(),
			Status:        "pending",
			NextAttemptAt: now,
			CreatedAt:     now,
		}
		if err := s.repo.CreateCommandWithOutbox(ctx, cmd, ob); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			return nil, err
		}

		observability.CommandsIssued.WithLabelValues(string(req.Origin), string(req.Action)).Inc()
		_ = realtime.Publish(ctx, s.rdb, realtime.Envelope{
			Type:     "command.issued",
			DeviceID: device.DeviceID,
			Data: map[string]any{
				"command_id": commandID,
				"action":     req.Action,
				"origin":     req.Origin,
				"status":     cmd.Status,
				"payload":    req.Payload,
			},
			Timestamp: now,
		})
		return cmd, nil
	}
	return nil, fmt.Errorf("could not allocate unique command id")
}

func newCommandID(now time.Time) string {
	return fmt.Sprintf("cmd-%s-%04d", now.Format("20060102"), rand.Intn(10000))
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
