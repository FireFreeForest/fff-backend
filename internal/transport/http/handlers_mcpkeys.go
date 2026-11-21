package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

type createdMCPKeyResponse struct {
	domain.MCPAPIKey
	Token string `json:"token"`
}

type rotatedMCPKeyResponse struct {
	Key      createdMCPKeyResponse `json:"key"`
	Previous domain.MCPAPIKey      `json:"previous"`
}

func (h *Handler) listMCPKeys(c *gin.Context) {
	if h.keys == nil {
		respondError(c, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi")
		return
	}
	page, perPage, limit, offset := pageParams(c)
	rows, total, err := h.keys.List(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("list mcp keys failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, rows, page, perPage, total)
}

func (h *Handler) createMCPKey(c *gin.Context) {
	if h.keys == nil {
		respondError(c, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi")
		return
	}
	actor, _, _ := currentUser(c)
	var req struct {
		Name      string  `json:"name"`
		Role      string  `json:"role"`
		ActorID   *string `json:"actor_id"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Payload API key tidak valid")
		return
	}
	actorID, ok := parseOptionalUUID(c, req.ActorID, "actor_id")
	if !ok {
		return
	}
	expiresAt, ok := parseOptionalTimeField(c, req.ExpiresAt, "expires_at")
	if !ok {
		return
	}
	created, err := h.keys.Create(c.Request.Context(), service.CreateMCPKeyInput{
		Name:      req.Name,
		Role:      domain.UserRole(strings.ToLower(strings.TrimSpace(req.Role))),
		ActorID:   actorID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		h.respondMCPKeyError(c, err)
		return
	}
	h.audit(c, &actor, "mcp_key.create", "mcp_key", created.Key.ID.String(), map[string]any{
		"name": created.Key.Name,
		"role": created.Key.Role,
	})
	respondData(c, http.StatusCreated, createdMCPKeyResponse{
		MCPAPIKey: *created.Key,
		Token:     created.Token,
	})
}

func (h *Handler) updateMCPKey(c *gin.Context) {
	if h.keys == nil {
		respondError(c, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi")
		return
	}
	actor, _, _ := currentUser(c)
	id, ok := parseUUIDParam(c, "keyID")
	if !ok {
		return
	}
	var req struct {
		Name           *string `json:"name"`
		Role           *string `json:"role"`
		ActorID        *string `json:"actor_id"`
		ClearActorID   bool    `json:"clear_actor_id"`
		ExpiresAt      *string `json:"expires_at"`
		ClearExpiresAt bool    `json:"clear_expires_at"`
		IsActive       *bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Payload API key tidak valid")
		return
	}

	in := service.UpdateMCPKeyInput{
		Name:        req.Name,
		ClearActor:  req.ClearActorID,
		ClearExpiry: req.ClearExpiresAt,
		IsActive:    req.IsActive,
	}
	if req.Role != nil {
		role := domain.UserRole(strings.ToLower(strings.TrimSpace(*req.Role)))
		in.Role = &role
	}
	var okField bool
	in.ActorID, okField = parseOptionalUUID(c, req.ActorID, "actor_id")
	if !okField {
		return
	}
	in.ExpiresAt, okField = parseOptionalTimeField(c, req.ExpiresAt, "expires_at")
	if !okField {
		return
	}

	updated, err := h.keys.Update(c.Request.Context(), id, in)
	if err != nil {
		h.respondMCPKeyError(c, err)
		return
	}
	h.audit(c, &actor, "mcp_key.update", "mcp_key", id.String(), nil)
	respondData(c, http.StatusOK, updated)
}

func (h *Handler) rotateMCPKey(c *gin.Context) {
	if h.keys == nil {
		respondError(c, http.StatusServiceUnavailable, "mcp_unavailable", "MCP belum dikonfigurasi")
		return
	}
	actor, _, _ := currentUser(c)
	id, ok := parseUUIDParam(c, "keyID")
	if !ok {
		return
	}
	var req struct {
		GraceSeconds *int `json:"grace_seconds"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_request", "Payload rotasi tidak valid")
			return
		}
	}
	graceSeconds := 0
	if req.GraceSeconds != nil {
		graceSeconds = *req.GraceSeconds
	}
	if graceSeconds < 0 || graceSeconds > int((7*24*time.Hour).Seconds()) {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "grace_seconds harus antara 0 dan 604800")
		return
	}
	created, previous, err := h.keys.Rotate(c.Request.Context(), id, time.Duration(graceSeconds)*time.Second)
	if err != nil {
		h.respondMCPKeyError(c, err)
		return
	}
	h.audit(c, &actor, "mcp_key.rotate", "mcp_key", id.String(), map[string]any{
		"new_key_id":    created.Key.ID.String(),
		"grace_seconds": graceSeconds,
	})
	respondData(c, http.StatusOK, rotatedMCPKeyResponse{
		Key: createdMCPKeyResponse{
			MCPAPIKey: *created.Key,
			Token:     created.Token,
		},
		Previous: *previous,
	})
}

func (h *Handler) respondMCPKeyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		respondError(c, http.StatusNotFound, "not_found", "API key tidak ditemukan")
	case errors.Is(err, service.ErrValidation), errors.Is(err, service.ErrMCPKeyRoleMismatch):
		respondError(c, http.StatusUnprocessableEntity, "validation_error", err.Error())
	default:
		h.log.Error("mcp key operation failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
	}
}

func parseOptionalUUID(c *gin.Context, raw *string, field string) (*uuid.UUID, bool) {
	if raw == nil {
		return nil, true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, true
	}
	id, err := uuid.Parse(value)
	if err != nil {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", field+" harus berupa UUID")
		return nil, false
	}
	return &id, true
}

func parseOptionalTimeField(c *gin.Context, raw *string, field string) (*time.Time, bool) {
	if raw == nil {
		return nil, true
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", field+" harus dalam format RFC3339")
		return nil, false
	}
	t = t.UTC()
	return &t, true
}
