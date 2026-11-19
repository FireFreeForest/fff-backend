package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventTimelineKind string

const (
	TimelineEventCreated EventTimelineKind = "CREATED"
	TimelineNotification EventTimelineKind = "NOTIFICATION"
	TimelineReminder     EventTimelineKind = "REMINDER"
	TimelineEscalation   EventTimelineKind = "ESCALATION"
	TimelineAcknowledged EventTimelineKind = "ACKNOWLEDGED"
	TimelineResolved     EventTimelineKind = "RESOLVED"
	TimelineAutoResolved EventTimelineKind = "AUTO_RESOLVED"
)

type EventTimelineOutcome string

const (
	TimelineOutcomeSent    EventTimelineOutcome = "sent"
	TimelineOutcomePartial EventTimelineOutcome = "partial"
	TimelineOutcomeFailed  EventTimelineOutcome = "failed"
	TimelineOutcomeSkipped EventTimelineOutcome = "skipped"
)

type EventTimelineActorType string

const (
	TimelineActorSystem EventTimelineActorType = "system"
	TimelineActorUser   EventTimelineActorType = "user"
)

type EventTimelineLevel string

const (
	TimelineLevelInfo     EventTimelineLevel = "info"
	TimelineLevelSuccess  EventTimelineLevel = "success"
	TimelineLevelWarning  EventTimelineLevel = "warning"
	TimelineLevelError    EventTimelineLevel = "error"
	TimelineLevelCritical EventTimelineLevel = "critical"
	TimelineLevelMuted    EventTimelineLevel = "muted"
)

type EventTimeline struct {
	ID         uint64                 `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID    uuid.UUID              `gorm:"type:uuid;not null" json:"event_id"`
	Kind       EventTimelineKind      `gorm:"type:text;not null" json:"kind"`
	Outcome    *EventTimelineOutcome  `gorm:"type:text" json:"outcome,omitempty"`
	ActorType  EventTimelineActorType `gorm:"type:text;not null" json:"actor_type"`
	ActorID    *uuid.UUID             `gorm:"type:uuid" json:"actor_id,omitempty"`
	ActorName  string                 `gorm:"not null" json:"actor_name"`
	ActorRole  UserRole               `gorm:"type:text" json:"actor_role,omitempty"`
	Title      string                 `gorm:"not null" json:"title"`
	Message    string                 `gorm:"not null" json:"message"`
	Level      EventTimelineLevel     `gorm:"type:text;not null" json:"level"`
	Metadata   json.RawMessage        `gorm:"type:jsonb" json:"metadata,omitempty"`
	OccurredAt time.Time              `json:"occurred_at"`
	CreatedAt  time.Time              `json:"created_at"`
	DedupeKey  *string                `gorm:"type:text" json:"-"`
}

func (EventTimeline) TableName() string { return "event_timeline" }

func TimelineMetadata(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func EventTitle(eventType EventType) string {
	switch eventType {
	case EventFireDetected:
		return "Api Terdeteksi"
	case EventSmokeDetected:
		return "Asap Terdeteksi"
	case EventTemperatureHigh:
		return "Suhu Tinggi"
	case EventSoilDry:
		return "Tanah Kering"
	case EventDeviceOffline:
		return "Perangkat Offline"
	case EventSensorError:
		return "Error Sensor"
	default:
		return "Event Terdeteksi"
	}
}

func EventLevel(severity Severity) EventTimelineLevel {
	switch severity {
	case SeverityCritical:
		return TimelineLevelCritical
	case SeverityError:
		return TimelineLevelError
	case SeverityWarning:
		return TimelineLevelWarning
	default:
		return TimelineLevelInfo
	}
}

func NotificationTimelineTitle(kind EventTimelineKind, outcome EventTimelineOutcome) string {
	switch kind {
	case TimelineReminder:
		switch outcome {
		case TimelineOutcomeFailed:
			return "Peringatan Ulang Gagal Dikirim"
		case TimelineOutcomeSkipped:
			return "Peringatan Ulang Dilewati"
		case TimelineOutcomePartial:
			return "Peringatan Ulang Terkirim Sebagian"
		default:
			return "Peringatan Ulang Dikirim"
		}
	case TimelineEscalation:
		switch outcome {
		case TimelineOutcomeFailed:
			return "Eskalasi Darurat Gagal Dikirim"
		case TimelineOutcomeSkipped:
			return "Eskalasi Darurat Dilewati"
		case TimelineOutcomePartial:
			return "Eskalasi Darurat Terkirim Sebagian"
		default:
			return "Eskalasi Darurat Dikirim"
		}
	default:
		switch outcome {
		case TimelineOutcomeFailed:
			return "Peringatan Gagal Dikirim"
		case TimelineOutcomeSkipped:
			return "Peringatan Dilewati"
		case TimelineOutcomePartial:
			return "Peringatan Terkirim Sebagian"
		default:
			return "Peringatan Dikirim"
		}
	}
}

func NotificationTimelineLevel(outcome EventTimelineOutcome) EventTimelineLevel {
	switch outcome {
	case TimelineOutcomeFailed:
		return TimelineLevelError
	case TimelineOutcomeSkipped, TimelineOutcomePartial:
		return TimelineLevelWarning
	default:
		return TimelineLevelInfo
	}
}
