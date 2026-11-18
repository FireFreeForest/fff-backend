package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInactiveUser       = errors.New("user is inactive")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTokenReuse         = errors.New("refresh token reuse detected")
	ErrValidation         = errors.New("validation error")
)

type AuthService struct {
	repo *repository.Repository
	cfg  *config.Config
	log  *slog.Logger
}

func NewAuthService(repo *repository.Repository, cfg *config.Config, log *slog.Logger) *AuthService {
	return &AuthService{repo: repo, cfg: cfg, log: log}
}

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func (s *AuthService) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(hash), err
}

func (s *AuthService) CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *AuthService) Login(ctx context.Context, email, password, ip, userAgent string) (*TokenPair, *domain.User, error) {
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.audit(ctx, nil, "auth.login_failed", "user", "", map[string]any{"email": email}, ip)
			return nil, nil, ErrInvalidCredentials
		}
		return nil, nil, err
	}
	if !user.IsActive {
		return nil, nil, ErrInactiveUser
	}
	if !s.CheckPassword(user.PasswordHash, password) {
		s.audit(ctx, nil, "auth.login_failed", "user", user.ID.String(), map[string]any{"email": email}, ip)
		return nil, nil, ErrInvalidCredentials
	}

	pair, err := s.issueTokens(ctx, user, ip, userAgent)
	if err != nil {
		return nil, nil, err
	}
	s.audit(ctx, &user.ID, "auth.login", "user", user.ID.String(), nil, ip)
	return pair, user, nil
}

func (s *AuthService) Refresh(ctx context.Context, rawToken, ip, userAgent string) (*TokenPair, error) {
	hash := hashToken(rawToken)
	stored, err := s.repo.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	if stored.RevokedAt != nil {
		_ = s.repo.RevokeAllUserRefreshTokens(ctx, stored.UserID)
		s.audit(ctx, &stored.UserID, "auth.refresh_reuse", "refresh_token", stored.ID.String(), nil, ip)
		return nil, ErrTokenReuse
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, ErrInvalidToken
	}

	user, err := s.repo.GetUserByID(ctx, stored.UserID)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if !user.IsActive {
		return nil, ErrInactiveUser
	}

	if err := s.repo.RevokeRefreshToken(ctx, stored.ID); err != nil {
		return nil, err
	}
	pair, err := s.issueTokens(ctx, user, ip, userAgent)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, &user.ID, "auth.refresh", "user", user.ID.String(), nil, ip)
	return pair, nil
}

func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID, rawToken string) error {
	hash := hashToken(rawToken)
	stored, err := s.repo.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	if stored.UserID != userID {
		return nil
	}
	return s.repo.RevokeRefreshToken(ctx, stored.ID)
}

func (s *AuthService) ParseAccess(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", t.Method.Alg())
		}
		return []byte(s.cfg.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("fire-free-forest"))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Subject == "" || claims.Role == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (s *AuthService) SeedAdmin(ctx context.Context) error {
	if s.cfg.SeedAdminEmail == "" || s.cfg.SeedAdminPassword == "" {
		return nil
	}
	count, err := s.repo.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := s.HashPassword(s.cfg.SeedAdminPassword)
	if err != nil {
		return err
	}
	user := &domain.User{
		ID:           uuid.New(),
		Email:        strings.ToLower(strings.TrimSpace(s.cfg.SeedAdminEmail)),
		PasswordHash: hash,
		Name:         s.cfg.SeedAdminName,
		Role:         domain.RoleAdmin,
		IsActive:     true,
	}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return err
	}
	s.log.Info("seeded admin user", "email", user.Email)
	if s.cfg.SeedAdminPassword == "admin12345" && s.cfg.IsProduction() {
		s.log.Warn("default admin password is still in use, change it immediately")
	}
	return nil
}

type CreateUserInput struct {
	Email    string
	Password string
	Name     string
	Role     domain.UserRole
}

func (s *AuthService) CreateUser(ctx context.Context, in CreateUserInput, actorID *uuid.UUID, ip string) (*domain.User, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("%w: email is invalid", ErrValidation)
	}
	if len(in.Password) < 8 {
		return nil, fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	if !domain.ValidRole(in.Role) {
		return nil, fmt.Errorf("%w: role must be admin, petugas, or viewer", ErrValidation)
	}
	hash, err := s.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	user := &domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hash,
		Name:         strings.TrimSpace(in.Name),
		Role:         in.Role,
		IsActive:     true,
	}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	s.audit(ctx, actorID, "user.create", "user", user.ID.String(), map[string]any{"email": email, "role": in.Role}, ip)
	return user, nil
}

func (s *AuthService) ListUsers(ctx context.Context, query string, limit, offset int) ([]domain.User, int64, error) {
	return s.repo.ListUsers(ctx, repository.UserFilter{Query: query, Limit: limit, Offset: offset})
}

type UpdateUserInput struct {
	Name     *string
	Role     *domain.UserRole
	IsActive *bool
	Password *string
}

func (s *AuthService) UpdateUser(ctx context.Context, id uuid.UUID, in UpdateUserInput, actorID *uuid.UUID, ip string) (*domain.User, error) {
	fields := map[string]any{}
	if in.Name != nil {
		fields["name"] = strings.TrimSpace(*in.Name)
	}
	if in.Role != nil {
		if !domain.ValidRole(*in.Role) {
			return nil, fmt.Errorf("%w: role must be admin, petugas, or viewer", ErrValidation)
		}
		fields["role"] = string(*in.Role)
	}
	if in.IsActive != nil {
		fields["is_active"] = *in.IsActive
	}
	if in.Password != nil && *in.Password != "" {
		if len(*in.Password) < 8 {
			return nil, fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
		}
		hash, err := s.HashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		fields["password_hash"] = hash
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: no fields to update", ErrValidation)
	}
	if err := s.repo.UpdateUserFields(ctx, id, fields); err != nil {
		return nil, err
	}
	s.audit(ctx, actorID, "user.update", "user", id.String(), nil, ip)
	return s.repo.GetUserByID(ctx, id)
}

func (s *AuthService) issueTokens(ctx context.Context, user *domain.User, ip, userAgent string) (*TokenPair, error) {
	now := time.Now().UTC()
	accessExp := now.Add(s.cfg.AccessTTL)
	refreshExp := now.Add(s.cfg.RefreshTTL)

	claims := Claims{
		Role: string(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    "fire-free-forest",
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(accessExp),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	access, err := token.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return nil, err
	}

	rawRefresh, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateRefreshToken(ctx, &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: refreshExp,
		UserAgent: truncateString(userAgent, 500),
		IP:        ip,
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:      access,
		RefreshToken:     rawRefresh,
		TokenType:        "Bearer",
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
	}, nil
}

func (s *AuthService) audit(ctx context.Context, actorID *uuid.UUID, action, entity, entityID string, meta map[string]any, ip string) {
	var raw json.RawMessage
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			raw = b
		}
	}
	err := s.repo.CreateAuditLog(ctx, &domain.AuditLog{
		ActorID:   actorID,
		Action:    action,
		Entity:    entity,
		EntityID:  entityID,
		Metadata:  raw,
		IP:        ip,
		CreatedAt: time.Now(),
	})
	if err != nil {
		s.log.Warn("failed to write audit log", "action", action, "error", err)
	}
}

func newRefreshToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
