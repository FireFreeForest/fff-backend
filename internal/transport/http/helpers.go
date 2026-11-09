package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
)

const (
	ctxUserID = "auth.user_id"
	ctxRole   = "auth.role"
)

func respondData(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data})
}

func respondList(c *gin.Context, data any, page, perPage int, total int64) {
	c.JSON(http.StatusOK, gin.H{
		"data": data,
		"meta": gin.H{
			"page":     page,
			"per_page": perPage,
			"total":    total,
		},
	})
}

func respondError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

func abortError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

func pageParams(c *gin.Context) (page, perPage, limit, offset int) {
	page = atoiDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	perPage = atoiDefault(c.Query("per_page"), 20)
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	limit = perPage
	offset = (page - 1) * perPage
	return
}

func atoiDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func parseTimeQuery(c *gin.Context, key string) (*time.Time, error) {
	raw := c.Query(key)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

func parseUUIDParam(c *gin.Context, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(key))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_id", "ID tidak valid")
		return uuid.Nil, false
	}
	return id, true
}

func setAuth(c *gin.Context, userID uuid.UUID, role domain.UserRole) {
	c.Set(ctxUserID, userID)
	c.Set(ctxRole, role)
}

func currentUser(c *gin.Context) (uuid.UUID, domain.UserRole, bool) {
	rawID, ok := c.Get(ctxUserID)
	if !ok {
		return uuid.Nil, "", false
	}
	id, ok := rawID.(uuid.UUID)
	if !ok {
		return uuid.Nil, "", false
	}
	rawRole, _ := c.Get(ctxRole)
	role, _ := rawRole.(domain.UserRole)
	return id, role, true
}

func (h *Handler) audit(c *gin.Context, actor *uuid.UUID, action, entity, entityID string, meta map[string]any) {
	var raw json.RawMessage
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			raw = b
		}
	}
	err := h.repo.CreateAuditLog(c.Request.Context(), &domain.AuditLog{
		ActorID:   actor,
		Action:    action,
		Entity:    entity,
		EntityID:  entityID,
		Metadata:  raw,
		IP:        c.ClientIP(),
		CreatedAt: time.Now(),
	})
	if err != nil {
		h.log.Warn("failed to write audit log", "action", action, "error", err)
	}
}
