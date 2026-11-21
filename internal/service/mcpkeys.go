package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

const (
	MCPKeyTokenPrefix = "mcp_"
	mcpKeyPrefixBytes = 6
	mcpKeySecretBytes = 32
	mcpKeyNameMaxLen  = 100
)

var (
	ErrMCPKeyInvalid      = errors.New("mcp api key is invalid")
	ErrMCPKeyInactive     = errors.New("mcp api key is inactive")
	ErrMCPKeyExpired      = errors.New("mcp api key has expired")
	ErrMCPKeyRoleMismatch = errors.New("mcp api key role does not match actor role")
)

// MCPPrincipal is the authenticated identity attached to an MCP request.
type MCPPrincipal struct {
	KeyID   uuid.UUID
	KeyName string
	Role    domain.UserRole
	ActorID *uuid.UUID
}

type MCPKeyService struct {
	repo *repository.Repository
	now  func() time.Time
}

func NewMCPKeyService(repo *repository.Repository) *MCPKeyService {
	return &MCPKeyService{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

type CreateMCPKeyInput struct {
	Name      string
	Role      domain.UserRole
	ActorID   *uuid.UUID
	ExpiresAt *time.Time
}

type CreatedMCPKey struct {
	Key   *domain.MCPAPIKey
	Token string
}

type UpdateMCPKeyInput struct {
	Name        *string
	Role        *domain.UserRole
	ActorID     *uuid.UUID
	ClearActor  bool
	ExpiresAt   *time.Time
	ClearExpiry bool
	IsActive    *bool
}

func (s *MCPKeyService) Create(ctx context.Context, in CreateMCPKeyInput) (*CreatedMCPKey, error) {
	name, err := normalizeMCPKeyName(in.Name)
	if err != nil {
		return nil, err
	}
	if !domain.ValidRole(in.Role) {
		return nil, fmt.Errorf("%w: role must be admin, petugas, or viewer", ErrValidation)
	}
	if err := s.validateActor(ctx, in.ActorID, in.Role); err != nil {
		return nil, err
	}
	expiresAt, err := normalizeMCPKeyExpiry(in.ExpiresAt, s.now())
	if err != nil {
		return nil, err
	}

	now := s.now()
	for attempt := 0; attempt < 5; attempt++ {
		generated, err := generateMCPKey()
		if err != nil {
			return nil, err
		}
		key := &domain.MCPAPIKey{
			ID:        uuid.New(),
			Name:      name,
			KeyPrefix: generated.Prefix,
			KeyHash:   generated.Hash,
			Role:      in.Role,
			ActorID:   in.ActorID,
			IsActive:  true,
			ExpiresAt: expiresAt,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.repo.CreateMCPKey(ctx, key); err != nil {
			// A duplicate prefix is extremely unlikely, but retry instead of
			// failing the request if the database reports a conflict.
			if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateKey(err) {
				continue
			}
			return nil, err
		}
		return &CreatedMCPKey{Key: key, Token: generated.Token}, nil
	}
	return nil, fmt.Errorf("could not allocate unique mcp api key prefix")
}

func (s *MCPKeyService) List(ctx context.Context, limit, offset int) ([]domain.MCPAPIKey, int64, error) {
	return s.repo.ListMCPKeys(ctx, limit, offset)
}

func (s *MCPKeyService) Get(ctx context.Context, id uuid.UUID) (*domain.MCPAPIKey, error) {
	return s.repo.GetMCPKeyByID(ctx, id)
}

func (s *MCPKeyService) Update(ctx context.Context, id uuid.UUID, in UpdateMCPKeyInput) (*domain.MCPAPIKey, error) {
	key, err := s.repo.GetMCPKeyByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.ActorID != nil && in.ClearActor {
		return nil, fmt.Errorf("%w: actor_id and clear_actor_id cannot be used together", ErrValidation)
	}
	if in.ExpiresAt != nil && in.ClearExpiry {
		return nil, fmt.Errorf("%w: expires_at and clear_expires_at cannot be used together", ErrValidation)
	}

	fields := map[string]any{}
	if in.Name != nil {
		name, err := normalizeMCPKeyName(*in.Name)
		if err != nil {
			return nil, err
		}
		fields["name"] = name
	}
	nextRole := key.Role
	if in.Role != nil {
		if !domain.ValidRole(*in.Role) {
			return nil, fmt.Errorf("%w: role must be admin, petugas, or viewer", ErrValidation)
		}
		nextRole = *in.Role
		fields["role"] = *in.Role
	}
	nextActor := key.ActorID
	if in.ClearActor {
		nextActor = nil
		fields["actor_id"] = nil
	} else if in.ActorID != nil {
		nextActor = in.ActorID
		fields["actor_id"] = *in.ActorID
	}
	if err := s.validateActor(ctx, nextActor, nextRole); err != nil {
		return nil, err
	}
	if in.ClearExpiry {
		fields["expires_at"] = nil
	} else if in.ExpiresAt != nil {
		expiresAt, err := normalizeMCPKeyExpiry(in.ExpiresAt, s.now())
		if err != nil {
			return nil, err
		}
		fields["expires_at"] = expiresAt
	}
	if in.IsActive != nil {
		fields["is_active"] = *in.IsActive
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: no fields to update", ErrValidation)
	}
	if err := s.repo.UpdateMCPKeyFields(ctx, id, fields); err != nil {
		return nil, err
	}
	return s.repo.GetMCPKeyByID(ctx, id)
}

// Rotate creates a replacement key with the same metadata and either disables
// the old key or lets it expire after grace.
func (s *MCPKeyService) Rotate(ctx context.Context, id uuid.UUID, grace time.Duration) (*CreatedMCPKey, *domain.MCPAPIKey, error) {
	old, err := s.repo.GetMCPKeyByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !old.IsActive {
		return nil, nil, fmt.Errorf("%w: key tidak aktif; buat key baru", ErrValidation)
	}
	if old.ExpiresAt != nil && !old.ExpiresAt.After(s.now()) {
		return nil, nil, fmt.Errorf("%w: key sudah kedaluwarsa; buat key baru", ErrValidation)
	}
	created, err := s.Create(ctx, CreateMCPKeyInput{
		Name:      old.Name,
		Role:      old.Role,
		ActorID:   old.ActorID,
		ExpiresAt: old.ExpiresAt,
	})
	if err != nil {
		return nil, nil, err
	}

	fields := map[string]any{}
	if grace <= 0 {
		fields["is_active"] = false
	} else {
		expireAt := s.now().Add(grace)
		// Grace never extends the original expiry.
		if old.ExpiresAt != nil && old.ExpiresAt.Before(expireAt) {
			expireAt = *old.ExpiresAt
		}
		fields["expires_at"] = expireAt
	}
	if err := s.repo.UpdateMCPKeyFields(ctx, old.ID, fields); err != nil {
		return nil, nil, err
	}
	refreshed, err := s.repo.GetMCPKeyByID(ctx, old.ID)
	if err != nil {
		return nil, nil, err
	}
	return created, refreshed, nil
}

// Authenticate resolves a bearer token to a principal. It never reveals
// whether a prefix exists: all credential failures return ErrMCPKeyInvalid.
func (s *MCPKeyService) Authenticate(ctx context.Context, token string) (*MCPPrincipal, error) {
	prefix, secret, ok := ParseMCPToken(token)
	if !ok {
		return nil, ErrMCPKeyInvalid
	}
	key, err := s.repo.GetMCPKeyByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrMCPKeyInvalid
		}
		return nil, err
	}
	if subtle.ConstantTimeCompare(hashMCPSecret(secret), key.KeyHash) != 1 {
		return nil, ErrMCPKeyInvalid
	}
	if !key.IsActive {
		return nil, ErrMCPKeyInactive
	}
	now := s.now()
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return nil, ErrMCPKeyExpired
	}
	_ = s.repo.TouchMCPKey(ctx, key.ID, now)
	return &MCPPrincipal{
		KeyID:   key.ID,
		KeyName: key.Name,
		Role:    key.Role,
		ActorID: key.ActorID,
	}, nil
}

func (s *MCPKeyService) validateActor(ctx context.Context, actorID *uuid.UUID, role domain.UserRole) error {
	if actorID == nil {
		return nil
	}
	user, err := s.repo.GetUserByID(ctx, *actorID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("%w: actor tidak ditemukan", ErrValidation)
		}
		return err
	}
	if !user.IsActive {
		return fmt.Errorf("%w: actor tidak aktif", ErrValidation)
	}
	if user.Role != role {
		return fmt.Errorf("%w: role key (%s) harus sama dengan role actor (%s)", ErrMCPKeyRoleMismatch, role, user.Role)
	}
	return nil
}

func normalizeMCPKeyName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: name wajib diisi", ErrValidation)
	}
	if utf8.RuneCountInString(name) > mcpKeyNameMaxLen {
		return "", fmt.Errorf("%w: name maksimal %d karakter", ErrValidation, mcpKeyNameMaxLen)
	}
	return name, nil
}

func normalizeMCPKeyExpiry(raw *time.Time, now time.Time) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	expiresAt := raw.UTC()
	if !expiresAt.After(now) {
		return nil, fmt.Errorf("%w: expires_at harus di masa depan", ErrValidation)
	}
	return &expiresAt, nil
}

// NormalizeMCPKeyPrefix accepts either "mcp_<hex>" or a bare prefix and
// returns the canonical stored form.
func NormalizeMCPKeyPrefix(raw string) (string, bool) {
	prefix := strings.ToLower(strings.TrimSpace(raw))
	prefix = strings.TrimPrefix(prefix, MCPKeyTokenPrefix)
	if len(prefix) != mcpKeyPrefixBytes*2 {
		return "", false
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return "", false
	}
	return MCPKeyTokenPrefix + prefix, true
}

type generatedMCPKey struct {
	Token  string
	Prefix string
	Hash   []byte
}

func generateMCPKey() (generatedMCPKey, error) {
	prefixBytes := make([]byte, mcpKeyPrefixBytes)
	secretBytes := make([]byte, mcpKeySecretBytes)
	if _, err := rand.Read(prefixBytes); err != nil {
		return generatedMCPKey{}, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return generatedMCPKey{}, err
	}
	prefix := MCPKeyTokenPrefix + hex.EncodeToString(prefixBytes)
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	return generatedMCPKey{
		Token:  prefix + "_" + secret,
		Prefix: prefix,
		Hash:   hashMCPSecret(secret),
	}, nil
}

// ParseMCPToken splits "mcp_<hex>_<secret>" into its stored prefix and secret.
func ParseMCPToken(token string) (prefix, secret string, ok bool) {
	token = strings.TrimSpace(token)
	rest, found := strings.CutPrefix(token, MCPKeyTokenPrefix)
	if !found {
		return "", "", false
	}
	hexPart, secret, found := strings.Cut(rest, "_")
	if !found || secret == "" || len(hexPart) != mcpKeyPrefixBytes*2 {
		return "", "", false
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", "", false
	}
	return MCPKeyTokenPrefix + hexPart, secret, true
}

func hashMCPSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func isDuplicateKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}
