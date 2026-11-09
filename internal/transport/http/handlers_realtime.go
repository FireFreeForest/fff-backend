package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"firefreeforest/backend/internal/domain"
)

func (h *Handler) registerPushToken(c *gin.Context) {
	userID, _, _ := currentUser(c)
	var req struct {
		Token    string `json:"token" binding:"required"`
		Platform string `json:"platform"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "token FCM wajib diisi")
		return
	}
	platform := strings.ToLower(strings.TrimSpace(req.Platform))
	if platform == "" {
		platform = "android"
	}
	token := &domain.PushToken{
		ID:       uuid.New(),
		UserID:   userID,
		Token:    strings.TrimSpace(req.Token),
		Platform: platform,
	}
	if err := h.repo.UpsertPushToken(c.Request.Context(), token); err != nil {
		h.log.Error("register push token failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) deletePushToken(c *gin.Context) {
	userID, _, _ := currentUser(c)
	token := c.Param("token")
	if strings.TrimSpace(token) == "" {
		respondError(c, http.StatusBadRequest, "invalid_request", "token tidak valid")
		return
	}
	if err := h.repo.DeletePushToken(c.Request.Context(), userID, token); err != nil {
		h.log.Error("delete push token failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) wsTicket(c *gin.Context) {
	userID, _, _ := currentUser(c)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", "Gagal membuat tiket WebSocket")
		return
	}
	ticket := hex.EncodeToString(buf)
	key := "ff:ws:ticket:" + ticket
	if err := h.rdb.Set(c.Request.Context(), key, userID.String(), time.Minute).Err(); err != nil {
		h.log.Error("create ws ticket failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, gin.H{"ticket": ticket, "expires_in": 60})
}

func (h *Handler) websocket(c *gin.Context) {
	ticket := strings.TrimSpace(c.Query("ticket"))
	if ticket == "" {
		respondError(c, http.StatusUnauthorized, "unauthorized", "Tiket WebSocket diperlukan")
		return
	}
	key := "ff:ws:ticket:" + ticket
	value, err := h.rdb.GetDel(c.Request.Context(), key).Result()
	if err != nil || value == "" {
		respondError(c, http.StatusUnauthorized, "invalid_ticket", "Tiket WebSocket tidak valid atau kedaluwarsa")
		return
	}
	if _, err := uuid.Parse(value); err != nil {
		respondError(c, http.StatusUnauthorized, "invalid_ticket", "Tiket WebSocket tidak valid")
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     h.checkWSOrigin,
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Warn("websocket upgrade failed", "error", err)
		return
	}
	filter := strings.TrimSpace(c.Query("device_id"))
	h.hub.Serve(conn, filter)
}

func (h *Handler) checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || h.cfg.WSAllowedOrigin == "*" {
		return true
	}
	if origin == h.cfg.WSAllowedOrigin {
		return true
	}
	for _, allowed := range h.cfg.CORSOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}
