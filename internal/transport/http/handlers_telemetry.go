package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) listEnvironment(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
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
	page, perPage, limit, offset := pageParams(c)
	rows, total, err := h.repo.ListEnvironment(c.Request.Context(), device.ID, from, to, limit, offset)
	if err != nil {
		h.log.Error("list environment telemetry failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, rows, page, perPage, total)
}

func (h *Handler) listFire(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
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
	page, perPage, limit, offset := pageParams(c)
	rows, total, err := h.repo.ListFire(c.Request.Context(), device.ID, from, to, limit, offset)
	if err != nil {
		h.log.Error("list fire telemetry failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, rows, page, perPage, total)
}
