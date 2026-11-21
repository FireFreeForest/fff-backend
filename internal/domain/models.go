package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	RoleAdmin   UserRole = "admin"
	RolePetugas UserRole = "petugas"
	RoleViewer  UserRole = "viewer"
)

func ValidRole(r UserRole) bool {
	switch r {
	case RoleAdmin, RolePetugas, RoleViewer:
		return true
	}
	return false
}

type DeviceStatus string

const (
	DeviceOnline  DeviceStatus = "online"
	DeviceOffline DeviceStatus = "offline"
)

type Severity string

const (
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func SeverityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 1
	case SeverityWarning:
		return 2
	case SeverityCritical:
		return 3
	}
	return 0
}

type EventType string

const (
	EventFireDetected    EventType = "FIRE_DETECTED"
	EventSmokeDetected   EventType = "SMOKE_DETECTED"
	EventSoilDry         EventType = "SOIL_DRY"
	EventTemperatureHigh EventType = "TEMPERATURE_HIGH"
	EventSensorError     EventType = "SENSOR_ERROR"
	EventDeviceOffline   EventType = "DEVICE_OFFLINE"
)

func (t EventType) Severity() Severity {
	switch t {
	case EventFireDetected:
		return SeverityCritical
	case EventSensorError:
		return SeverityError
	default:
		return SeverityWarning
	}
}

func ValidEventType(t EventType) bool {
	switch t {
	case EventFireDetected, EventSmokeDetected, EventSoilDry, EventTemperatureHigh, EventSensorError, EventDeviceOffline:
		return true
	}
	return false
}

type EventStatus string

const (
	EventActive       EventStatus = "active"
	EventAcknowledged EventStatus = "acknowledged"
	EventResolved     EventStatus = "resolved"
)

type CommandStatus string

const (
	CommandPending CommandStatus = "pending"
	CommandSent    CommandStatus = "sent"
	CommandSuccess CommandStatus = "success"
	CommandFailed  CommandStatus = "failed"
	CommandTimeout CommandStatus = "timeout"
)

type CommandAction string

const (
	ActionSetPump CommandAction = "SET_PUMP"
)

type CommandOrigin string

const (
	OriginManual CommandOrigin = "manual"
	OriginSystem CommandOrigin = "system"
	OriginMCP    CommandOrigin = "mcp"
)

type User struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Email        string    `gorm:"not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Name         string    `json:"name"`
	Role         UserRole  `gorm:"type:text;not null" json:"role"`
	IsActive     bool      `gorm:"not null" json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RefreshToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash string     `gorm:"not null" json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	UserAgent string     `json:"user_agent"`
	IP        string     `json:"ip"`
	CreatedAt time.Time  `json:"created_at"`
}

type Device struct {
	ID              uuid.UUID    `gorm:"type:uuid;primaryKey" json:"id"`
	DeviceID        string       `gorm:"not null" json:"device_id"`
	Name            string       `json:"name"`
	LocationName    string       `json:"location_name"`
	Latitude        *float64     `json:"latitude,omitempty"`
	Longitude       *float64     `json:"longitude,omitempty"`
	Status          DeviceStatus `gorm:"type:text;not null" json:"status"`
	FirmwareVersion string       `json:"firmware_version"`
	LastSeenAt      *time.Time   `json:"last_seen_at,omitempty"`
	OnlineSince     *time.Time   `json:"online_since,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

func (d Device) UptimeSeconds(now time.Time) int64 {
	if d.Status != DeviceOnline || d.OnlineSince == nil {
		return 0
	}
	seconds := int64(now.Sub(*d.OnlineSince).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (d Device) MarshalJSON() ([]byte, error) {
	type alias Device
	return json.Marshal(struct {
		alias
		UptimeSeconds int64 `json:"uptime_seconds"`
	}{
		alias:         alias(d),
		UptimeSeconds: d.UptimeSeconds(time.Now().UTC()),
	})
}

type TelemetryEnvironment struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID     uuid.UUID `gorm:"type:uuid;not null" json:"device_id"`
	Temperature  *float64  `json:"temperature"`
	Humidity     *float64  `json:"humidity"`
	SoilMoisture float64   `json:"soil_moisture"`
	RecordedAt   time.Time `json:"recorded_at"`
	ReceivedAt   time.Time `json:"received_at"`
}

func (TelemetryEnvironment) TableName() string { return "telemetry_environment" }

type TelemetryFire struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	DeviceID      uuid.UUID `gorm:"type:uuid;not null" json:"device_id"`
	FlameDetected bool      `json:"flame_detected"`
	FlameLevel    *int      `json:"flame_level,omitempty"`
	SmokeLevel    int       `json:"smoke_level"`
	Pump          bool      `json:"pump"`
	Buzzer        bool      `json:"buzzer"`
	RecordedAt    time.Time `json:"recorded_at"`
	ReceivedAt    time.Time `json:"received_at"`
}

func (TelemetryFire) TableName() string { return "telemetry_fire" }

type TelemetryEnvironmentBucket struct {
	BucketStart  time.Time `json:"bucket_start"`
	Temperature  *float64  `json:"temperature"`
	Humidity     *float64  `json:"humidity"`
	SoilMoisture float64   `json:"soil_moisture"`
	SampleCount  int64     `json:"sample_count"`
}

type TelemetryFireBucket struct {
	BucketStart        time.Time `json:"bucket_start"`
	FlameLevel         *float64  `json:"flame_level"`
	SmokeLevel         float64   `json:"smoke_level"`
	FlameDetectedRatio float64   `json:"flame_detected_ratio"`
	PumpOnRatio        float64   `json:"pump_on_ratio"`
	BuzzerOnRatio      float64   `json:"buzzer_on_ratio"`
	SampleCount        int64     `json:"sample_count"`
}

type Event struct {
	ID               uuid.UUID       `gorm:"type:uuid;primaryKey" json:"id"`
	DeviceID         uuid.UUID       `gorm:"type:uuid;not null" json:"device_id"`
	Device           *Device         `gorm:"foreignKey:DeviceID;references:ID;belongsTo" json:"device,omitempty"`
	EventType        EventType       `gorm:"type:text;not null" json:"event_type"`
	Severity         Severity        `gorm:"type:text;not null" json:"severity"`
	Source           string          `json:"source"`
	Message          string          `json:"message"`
	Status           EventStatus     `gorm:"type:text;not null" json:"status"`
	Metadata         json.RawMessage `gorm:"type:jsonb" json:"metadata,omitempty"`
	TriggeredAt      time.Time       `json:"triggered_at"`
	AcknowledgedBy   *uuid.UUID      `gorm:"type:uuid" json:"acknowledged_by,omitempty"`
	AcknowledgedAt   *time.Time      `json:"acknowledged_at,omitempty"`
	ResolvedBy       *uuid.UUID      `gorm:"type:uuid" json:"resolved_by,omitempty"`
	ResolvedAt       *time.Time      `json:"resolved_at,omitempty"`
	ReminderSentAt   *time.Time      `gorm:"column:reminder_sent_at" json:"-"`
	EscalationSentAt *time.Time      `gorm:"column:escalation_sent_at" json:"-"`
	CreatedAt        time.Time       `json:"created_at"`
}

type Command struct {
	ID         uuid.UUID       `gorm:"type:uuid;primaryKey" json:"id"`
	CommandID  string          `gorm:"not null" json:"command_id"`
	DeviceID   uuid.UUID       `gorm:"type:uuid;not null" json:"device_id"`
	Action     CommandAction   `gorm:"type:text;not null" json:"action"`
	Payload    json.RawMessage `gorm:"type:jsonb;not null" json:"payload"`
	Status     CommandStatus   `gorm:"type:text;not null" json:"status"`
	Origin     CommandOrigin   `gorm:"type:text;not null" json:"origin"`
	IssuedBy   *uuid.UUID      `gorm:"type:uuid" json:"issued_by,omitempty"`
	IssuedAt   time.Time       `json:"issued_at"`
	SentAt     *time.Time      `json:"sent_at,omitempty"`
	AckedAt    *time.Time      `json:"acked_at,omitempty"`
	AckMessage string          `json:"ack_message"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type Outbox struct {
	ID            uuid.UUID       `gorm:"type:uuid;primaryKey" json:"id"`
	Topic         string          `gorm:"not null" json:"topic"`
	Payload       json.RawMessage `gorm:"type:jsonb;not null" json:"payload"`
	QoS           int             `gorm:"column:qos;not null" json:"qos"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Status        string          `gorm:"type:text;not null" json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	CreatedAt     time.Time       `json:"created_at"`
	SentAt        *time.Time      `json:"sent_at,omitempty"`
}

func (Outbox) TableName() string { return "outbox" }

type PushToken struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID     uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	Token      string    `gorm:"not null" json:"token"`
	Platform   string    `json:"platform"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type AuditLog struct {
	ID        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ActorID   *uuid.UUID      `gorm:"type:uuid" json:"actor_id,omitempty"`
	Action    string          `gorm:"not null" json:"action"`
	Entity    string          `json:"entity"`
	EntityID  string          `json:"entity_id"`
	Metadata  json.RawMessage `gorm:"type:jsonb" json:"metadata,omitempty"`
	IP        string          `json:"ip"`
	CreatedAt time.Time       `json:"created_at"`
}

// MCPAPIKey is a database-backed credential for the MCP transports. Only the
// SHA-256 hash of the secret is stored; the raw token is shown once on create.
type MCPAPIKey struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	Name       string     `gorm:"not null" json:"name"`
	KeyPrefix  string     `gorm:"not null" json:"key_prefix"`
	KeyHash    []byte     `gorm:"not null" json:"-"`
	Role       UserRole   `gorm:"type:text;not null" json:"role"`
	ActorID    *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	IsActive   bool       `gorm:"not null" json:"is_active"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (MCPAPIKey) TableName() string { return "mcp_api_keys" }
