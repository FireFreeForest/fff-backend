package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"firefreeforest/backend/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Ping(ctx context.Context) error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func normalizeErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// ---------------------------------------------------------------- users

func (r *Repository) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &u, nil
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).Where("lower(email) = lower(?)", strings.TrimSpace(email)).First(&u).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &u, nil
}

func (r *Repository) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.User{}).Count(&n).Error
	return n, err
}

func (r *Repository) CreateUser(ctx context.Context, u *domain.User) error {
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *Repository) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&domain.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.User
	err := r.db.WithContext(ctx).Order("created_at ASC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) UpdateUserFields(ctx context.Context, id uuid.UUID, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&domain.User{}).Where("id = ?", id).Updates(fields).Error
}

// ---------------------------------------------------------------- refresh tokens

func (r *Repository) CreateRefreshToken(ctx context.Context, t *domain.RefreshToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repository) GetRefreshTokenByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	if err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &t, nil
}

func (r *Repository) RevokeRefreshToken(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&domain.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now).Error
}

func (r *Repository) RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&domain.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

// ---------------------------------------------------------------- devices

func (r *Repository) GetDeviceByID(ctx context.Context, id uuid.UUID) (*domain.Device, error) {
	var d domain.Device
	if err := r.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &d, nil
}

func (r *Repository) GetDeviceByDeviceID(ctx context.Context, deviceID string) (*domain.Device, error) {
	var d domain.Device
	res := r.db.WithContext(ctx).Where("device_id = ?", deviceID).Limit(1).Find(&d)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &d, nil
}

func (r *Repository) CreateDevice(ctx context.Context, d *domain.Device) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// EnsureDevice inserts the device if its device_id does not exist yet. It
// returns created=false and the existing row when another ingestion path won
// the race, avoiding duplicate-key errors in the MQTT consumer.
func (r *Repository) EnsureDevice(ctx context.Context, d *domain.Device) (bool, *domain.Device, error) {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "device_id"}},
		DoNothing: true,
	}).Create(d)
	if res.Error != nil {
		return false, nil, res.Error
	}
	if res.RowsAffected > 0 {
		return true, d, nil
	}
	existing, err := r.GetDeviceByDeviceID(ctx, d.DeviceID)
	if err != nil {
		return false, nil, err
	}
	return false, existing, nil
}

func (r *Repository) UpdateDeviceFields(ctx context.Context, id uuid.UUID, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&domain.Device{}).Where("id = ?", id).Updates(fields).Error
}

func (r *Repository) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	err := r.db.WithContext(ctx).Delete(&domain.Device{}, "id = ?", id).Error
	if err != nil {
		return err
	}
	return nil
}

func (r *Repository) ListDevices(ctx context.Context, limit, offset int) ([]domain.Device, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&domain.Device{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Device
	err := r.db.WithContext(ctx).Order("created_at ASC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) TouchDevice(ctx context.Context, deviceID string, now time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.Device{}).Where("device_id = ?", deviceID).
		Updates(map[string]any{
			"status":       string(domain.DeviceOnline),
			"last_seen_at": now,
			"updated_at":   now,
		}).Error
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

func (r *Repository) CountDevicesByStatus(ctx context.Context) (map[string]int64, error) {
	var rows []StatusCount
	err := r.db.WithContext(ctx).Model(&domain.Device{}).
		Select("status, count(*) AS count").Group("status").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{"online": 0, "offline": 0}
	for _, row := range rows {
		out[row.Status] = row.Count
	}
	return out, nil
}

type DeviceIDRow struct {
	DeviceID string `gorm:"column:device_id"`
}

func (r *Repository) MarkStaleDevicesOffline(ctx context.Context, cutoff time.Time) ([]string, error) {
	var rows []DeviceIDRow
	err := r.db.WithContext(ctx).Raw(`
		UPDATE devices
		SET status = 'offline', updated_at = now()
		WHERE status = 'online' AND (last_seen_at IS NULL OR last_seen_at < ?)
		RETURNING device_id`, cutoff).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.DeviceID)
	}
	return out, nil
}

// ---------------------------------------------------------------- telemetry

func (r *Repository) CreateEnvironment(ctx context.Context, t *domain.TelemetryEnvironment) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repository) CreateFire(ctx context.Context, t *domain.TelemetryFire) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repository) ListEnvironment(ctx context.Context, deviceID uuid.UUID, from, to *time.Time, limit, offset int) ([]domain.TelemetryEnvironment, int64, error) {
	build := func() *gorm.DB {
		q := r.db.WithContext(ctx).Model(&domain.TelemetryEnvironment{}).Where("device_id = ?", deviceID)
		if from != nil {
			q = q.Where("recorded_at >= ?", *from)
		}
		if to != nil {
			q = q.Where("recorded_at <= ?", *to)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.TelemetryEnvironment
	err := build().Order("recorded_at DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) ListFire(ctx context.Context, deviceID uuid.UUID, from, to *time.Time, limit, offset int) ([]domain.TelemetryFire, int64, error) {
	build := func() *gorm.DB {
		q := r.db.WithContext(ctx).Model(&domain.TelemetryFire{}).Where("device_id = ?", deviceID)
		if from != nil {
			q = q.Where("recorded_at >= ?", *from)
		}
		if to != nil {
			q = q.Where("recorded_at <= ?", *to)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.TelemetryFire
	err := build().Order("recorded_at DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) LatestEnvironment(ctx context.Context, deviceID uuid.UUID) (*domain.TelemetryEnvironment, error) {
	var row domain.TelemetryEnvironment
	if err := r.db.WithContext(ctx).Where("device_id = ?", deviceID).
		Order("recorded_at DESC").First(&row).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &row, nil
}

func (r *Repository) LatestFire(ctx context.Context, deviceID uuid.UUID) (*domain.TelemetryFire, error) {
	var row domain.TelemetryFire
	if err := r.db.WithContext(ctx).Where("device_id = ?", deviceID).
		Order("recorded_at DESC").First(&row).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &row, nil
}

func (r *Repository) DeleteTelemetryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	res := r.db.WithContext(ctx).Where("received_at < ?", cutoff).Delete(&domain.TelemetryEnvironment{})
	if res.Error != nil {
		return 0, res.Error
	}
	total += res.RowsAffected
	res = r.db.WithContext(ctx).Where("received_at < ?", cutoff).Delete(&domain.TelemetryFire{})
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	return total, nil
}

// ---------------------------------------------------------------- events

func (r *Repository) CreateEventWithOutbox(ctx context.Context, ev *domain.Event, ob *domain.Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if ev.ID == uuid.Nil {
			ev.ID = uuid.New()
		}
		if ob.ID == uuid.Nil {
			ob.ID = uuid.New()
		}
		if ob.AggregateID == "" {
			ob.AggregateID = ev.ID.String()
		}
		if ob.AggregateType == "" {
			ob.AggregateType = "event"
		}
		if err := tx.Create(ev).Error; err != nil {
			return err
		}
		return tx.Create(ob).Error
	})
}

func (r *Repository) GetEvent(ctx context.Context, id uuid.UUID) (*domain.Event, error) {
	var ev domain.Event
	if err := r.db.WithContext(ctx).First(&ev, "id = ?", id).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &ev, nil
}

type EventFilter struct {
	DeviceID  *uuid.UUID
	EventType string
	Severity  string
	Status    string
	From      *time.Time
	To        *time.Time
	Limit     int
	Offset    int
}

func (r *Repository) ListEvents(ctx context.Context, f EventFilter) ([]domain.Event, int64, error) {
	build := func() *gorm.DB {
		q := r.db.WithContext(ctx).Model(&domain.Event{})
		if f.DeviceID != nil {
			q = q.Where("device_id = ?", *f.DeviceID)
		}
		if f.EventType != "" {
			q = q.Where("event_type = ?", f.EventType)
		}
		if f.Severity != "" {
			q = q.Where("severity = ?", f.Severity)
		}
		if f.Status != "" {
			q = q.Where("status = ?", f.Status)
		}
		if f.From != nil {
			q = q.Where("triggered_at >= ?", *f.From)
		}
		if f.To != nil {
			q = q.Where("triggered_at <= ?", *f.To)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Event
	err := build().Order("triggered_at DESC").Limit(f.Limit).Offset(f.Offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) ListActiveEventsForDevice(ctx context.Context, deviceID uuid.UUID) ([]domain.Event, error) {
	var rows []domain.Event
	err := r.db.WithContext(ctx).
		Where("device_id = ? AND status IN ?", deviceID, []string{string(domain.EventActive), string(domain.EventAcknowledged)}).
		Order("triggered_at DESC").Find(&rows).Error
	return rows, err
}

func (r *Repository) AcknowledgeEvent(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&domain.Event{}).
		Where("id = ? AND status = ?", id, domain.EventActive).
		Updates(map[string]any{
			"status":          string(domain.EventAcknowledged),
			"acknowledged_by": userID,
			"acknowledged_at": now,
		})
	return res.RowsAffected > 0, res.Error
}

func (r *Repository) ResolveEvent(ctx context.Context, id uuid.UUID) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&domain.Event{}).
		Where("id = ? AND status IN ?", id, []string{string(domain.EventActive), string(domain.EventAcknowledged)}).
		Updates(map[string]any{
			"status":      string(domain.EventResolved),
			"resolved_at": now,
		})
	return res.RowsAffected > 0, res.Error
}

func (r *Repository) ResolveActiveEventByType(ctx context.Context, deviceID uuid.UUID, eventType domain.EventType) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&domain.Event{}).
		Where("device_id = ? AND event_type = ? AND status IN ?", deviceID, eventType,
			[]string{string(domain.EventActive), string(domain.EventAcknowledged)}).
		Updates(map[string]any{
			"status":      string(domain.EventResolved),
			"resolved_at": now,
		})
	return res.RowsAffected > 0, res.Error
}

type SeverityCount struct {
	Severity string `json:"severity"`
	Count    int64  `json:"count"`
}

func (r *Repository) ActiveEventSeverityCounts(ctx context.Context) (map[string]int64, int64, error) {
	var rows []SeverityCount
	err := r.db.WithContext(ctx).Model(&domain.Event{}).
		Select("severity, count(*) AS count").
		Where("status IN ?", []string{string(domain.EventActive), string(domain.EventAcknowledged)}).
		Group("severity").Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	out := map[string]int64{"error": 0, "warning": 0, "critical": 0}
	var total int64
	for _, row := range rows {
		out[row.Severity] = row.Count
		total += row.Count
	}
	return out, total, nil
}

// ---------------------------------------------------------------- commands

func (r *Repository) CreateCommandWithOutbox(ctx context.Context, cmd *domain.Command, ob *domain.Outbox) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if cmd.ID == uuid.Nil {
			cmd.ID = uuid.New()
		}
		if ob.ID == uuid.Nil {
			ob.ID = uuid.New()
		}
		if ob.AggregateID == "" {
			ob.AggregateID = cmd.ID.String()
		}
		if ob.AggregateType == "" {
			ob.AggregateType = "command"
		}
		if err := tx.Create(cmd).Error; err != nil {
			return err
		}
		return tx.Create(ob).Error
	})
}

func (r *Repository) GetCommandByCommandID(ctx context.Context, commandID string) (*domain.Command, error) {
	var cmd domain.Command
	if err := r.db.WithContext(ctx).Where("command_id = ?", commandID).First(&cmd).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &cmd, nil
}

func (r *Repository) ListCommands(ctx context.Context, deviceID uuid.UUID, limit, offset int) ([]domain.Command, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&domain.Command{}).Where("device_id = ?", deviceID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Command
	err := r.db.WithContext(ctx).Where("device_id = ?", deviceID).
		Order("issued_at DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) MarkCommandSent(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&domain.Command{}).
		Where("id = ? AND status = ?", id, domain.CommandPending).
		Updates(map[string]any{"status": string(domain.CommandSent), "sent_at": now, "updated_at": now}).Error
}

func (r *Repository) AckCommand(ctx context.Context, deviceID uuid.UUID, commandID string, status domain.CommandStatus, message string) (*domain.Command, bool, error) {
	var cmd domain.Command
	if err := r.db.WithContext(ctx).Where("command_id = ? AND device_id = ?", commandID, deviceID).First(&cmd).Error; err != nil {
		return nil, false, normalizeErr(err)
	}
	if cmd.Status == domain.CommandSuccess || cmd.Status == domain.CommandFailed || cmd.Status == domain.CommandTimeout {
		return &cmd, false, nil
	}
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&domain.Command{}).
		Where("id = ? AND status IN ?", cmd.ID, []string{string(domain.CommandPending), string(domain.CommandSent)}).
		Updates(map[string]any{
			"status":      string(status),
			"acked_at":    now,
			"ack_message": message,
			"updated_at":  now,
		})
	if res.Error != nil {
		return nil, false, res.Error
	}
	cmd.Status = status
	cmd.AckedAt = &now
	cmd.AckMessage = message
	return &cmd, res.RowsAffected > 0, nil
}

type CommandTimeoutRow struct {
	CommandID string    `gorm:"column:command_id"`
	DeviceID  uuid.UUID `gorm:"column:device_id"`
}

func (r *Repository) MarkCommandTimeouts(ctx context.Context, cutoff time.Time) ([]CommandTimeoutRow, error) {
	var rows []CommandTimeoutRow
	err := r.db.WithContext(ctx).Raw(`
		UPDATE commands
		SET status = 'timeout', updated_at = now()
		WHERE status IN ('pending', 'sent') AND issued_at < ?
		RETURNING command_id, device_id`, cutoff).Scan(&rows).Error
	return rows, err
}

// ---------------------------------------------------------------- outbox

func (r *Repository) ClaimOutbox(ctx context.Context, limit int) ([]domain.Outbox, error) {
	var rows []domain.Outbox
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND next_attempt_at <= ?", "pending", now).
			Order("created_at ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return tx.Model(&domain.Outbox{}).Where("id IN ?", ids).
			Update("next_attempt_at", now.Add(30*time.Second)).Error
	})
	return rows, err
}

func (r *Repository) MarkOutboxSent(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&domain.Outbox{}).Where("id = ?", id).
		Updates(map[string]any{"status": "sent", "sent_at": now, "last_error": ""}).Error
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, row domain.Outbox, errMsg string, maxAttempts int) error {
	attempts := row.Attempts + 1
	status := "pending"
	backoff := time.Duration(1<<min(attempts, 8)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	if attempts >= maxAttempts {
		status = "failed"
	}
	return r.db.WithContext(ctx).Model(&domain.Outbox{}).Where("id = ?", row.ID).
		Updates(map[string]any{
			"status":          status,
			"attempts":        attempts,
			"next_attempt_at": time.Now().Add(backoff),
			"last_error":      truncate(errMsg, 1000),
		}).Error
}

// ---------------------------------------------------------------- push tokens & audit

func (r *Repository) UpsertPushToken(ctx context.Context, t *domain.PushToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.LastUsedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "token"}},
		DoUpdates: clause.Assignments(map[string]any{
			"user_id":      t.UserID,
			"platform":     t.Platform,
			"last_used_at": now,
		}),
	}).Create(t).Error
}

func (r *Repository) DeletePushToken(ctx context.Context, userID uuid.UUID, token string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND token = ?", userID, token).
		Delete(&domain.PushToken{}).Error
}

func (r *Repository) DeleteInvalidPushTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("token IN ?", tokens).Delete(&domain.PushToken{}).Error
}

func (r *Repository) ListNotificationTokens(ctx context.Context, roles []domain.UserRole) ([]domain.PushToken, error) {
	roleStrings := make([]string, 0, len(roles))
	for _, role := range roles {
		roleStrings = append(roleStrings, string(role))
	}
	var rows []domain.PushToken
	err := r.db.WithContext(ctx).Table("push_tokens").
		Select("push_tokens.*").
		Joins("JOIN users ON users.id = push_tokens.user_id").
		Where("users.is_active = TRUE AND users.role IN ?", roleStrings).
		Find(&rows).Error
	return rows, err
}

func (r *Repository) CreateAuditLog(ctx context.Context, a *domain.AuditLog) error {
	if len(a.Metadata) == 0 {
		a.Metadata = json.RawMessage("{}")
	}
	return r.db.WithContext(ctx).Create(a).Error
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func fmtDeviceNotFound(deviceID string) error {
	return fmt.Errorf("device %s: %w", deviceID, ErrNotFound)
}
