package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/realtime"
	"firefreeforest/backend/internal/repository"
)

func (h *Handler) listEvents(c *gin.Context) {
	filter := repository.EventFilter{
		EventType: strings.ToUpper(strings.TrimSpace(c.Query("event_type"))),
		Severity:  strings.ToLower(strings.TrimSpace(c.Query("severity"))),
		Status:    strings.ToLower(strings.TrimSpace(c.Query("status"))),
	}
	if raw := strings.TrimSpace(c.Query("device_id")); raw != "" {
		device, err := h.repo.GetDeviceByDeviceID(c.Request.Context(), raw)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				respondError(c, http.StatusNotFound, "not_found", "Device tidak ditemukan")
				return
			}
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
			return
		}
		filter.DeviceID = &device.ID
	}
	from, err := parseTimeQuery(c, "from")
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_time", "Format waktu 'from' harus RFC3339")
		return
	}
	to, err := parseTimeQuery(c, "to")
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_time", "Format waktu 'to' harus RFC3339")
		return
	}
	filter.From = from
	filter.To = to

	page, perPage, limit, offset := pageParams(c)
	filter.Limit = limit
	filter.Offset = offset

	rows, total, err := h.repo.ListEvents(c.Request.Context(), filter)
	if err != nil {
		h.log.Error("list events failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, rows, page, perPage, total)
}

func (h *Handler) getEvent(c *gin.Context) {
	id, ok := parseUUIDParam(c, "eventID")
	if !ok {
		return
	}
	event, err := h.repo.GetEvent(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Event tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, event)
}

func (h *Handler) acknowledgeEvent(c *gin.Context) {
	userID, _, _ := currentUser(c)
	id, ok := parseUUIDParam(c, "eventID")
	if !ok {
		return
	}
	event, err := h.repo.GetEvent(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Event tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	if event.Status != domain.EventActive {
		respondError(c, http.StatusConflict, "invalid_state", "Event tidak berada pada status active")
		return
	}
	updated, err := h.repo.AcknowledgeEvent(c.Request.Context(), id, userID)
	if err != nil {
		h.log.Error("acknowledge event failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	if !updated {
		respondError(c, http.StatusConflict, "invalid_state", "Event sudah diproses oleh pengguna lain")
		return
	}
	h.audit(c, &userID, "event.acknowledge", "event", id.String(), nil)
	h.publishEventUpdate(c, id, "event.updated")
	refreshed, _ := h.repo.GetEvent(c.Request.Context(), id)
	respondData(c, http.StatusOK, refreshed)
}

func (h *Handler) resolveEvent(c *gin.Context) {
	userID, _, _ := currentUser(c)
	id, ok := parseUUIDParam(c, "eventID")
	if !ok {
		return
	}
	event, err := h.repo.GetEvent(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Event tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	if event.Status == domain.EventResolved {
		respondError(c, http.StatusConflict, "invalid_state", "Event sudah resolved")
		return
	}
	updated, err := h.repo.ResolveEvent(c.Request.Context(), id)
	if err != nil {
		h.log.Error("resolve event failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	if !updated {
		respondError(c, http.StatusConflict, "invalid_state", "Event tidak dapat di-resolve")
		return
	}
	h.audit(c, &userID, "event.resolve", "event", id.String(), nil)
	h.publishEventUpdate(c, id, "event.resolved")
	refreshed, _ := h.repo.GetEvent(c.Request.Context(), id)
	respondData(c, http.StatusOK, refreshed)
}

func (h *Handler) publishEventUpdate(c *gin.Context, eventID uuid.UUID, eventType string) {
	event, err := h.repo.GetEvent(c.Request.Context(), eventID)
	if err != nil {
		return
	}
	device, err := h.repo.GetDeviceByID(c.Request.Context(), event.DeviceID)
	if err != nil {
		return
	}
	_ = realtime.Publish(c.Request.Context(), h.rdb, realtime.Envelope{
		Type:     eventType,
		DeviceID: device.DeviceID,
		Data: map[string]any{
			"event":     event,
			"device_id": device.DeviceID,
		},
	})
}
