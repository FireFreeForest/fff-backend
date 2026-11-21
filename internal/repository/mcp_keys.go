package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
)

func (r *Repository) CreateMCPKey(ctx context.Context, key *domain.MCPAPIKey) error {
	if key.ID == uuid.Nil {
		key.ID = uuid.New()
	}
	now := time.Now().UTC()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(key).Error
}

func (r *Repository) ListMCPKeys(ctx context.Context, limit, offset int) ([]domain.MCPAPIKey, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&domain.MCPAPIKey{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.MCPAPIKey
	err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&rows).Error
	return rows, total, err
}

func (r *Repository) GetMCPKeyByID(ctx context.Context, id uuid.UUID) (*domain.MCPAPIKey, error) {
	var key domain.MCPAPIKey
	if err := r.db.WithContext(ctx).First(&key, "id = ?", id).Error; err != nil {
		return nil, normalizeErr(err)
	}
	return &key, nil
}

func (r *Repository) GetMCPKeyByPrefix(ctx context.Context, prefix string) (*domain.MCPAPIKey, error) {
	var key domain.MCPAPIKey
	res := r.db.WithContext(ctx).Where("key_prefix = ?", prefix).Limit(1).Find(&key)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &key, nil
}

func (r *Repository) UpdateMCPKeyFields(ctx context.Context, id uuid.UUID, fields map[string]any) error {
	fields["updated_at"] = time.Now().UTC()
	return r.db.WithContext(ctx).Model(&domain.MCPAPIKey{}).Where("id = ?", id).Updates(fields).Error
}

// TouchMCPKey records key usage at most once per five minutes to avoid a write
// on every MCP request.
func (r *Repository) TouchMCPKey(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.MCPAPIKey{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", id, now.Add(-5*time.Minute)).
		UpdateColumn("last_used_at", now).Error
}

func (r *Repository) CountActiveMCPKeys(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&domain.MCPAPIKey{}).
		Where("is_active = ? AND (expires_at IS NULL OR expires_at > ?)", true, time.Now().UTC()).
		Count(&total).Error
	return total, err
}
