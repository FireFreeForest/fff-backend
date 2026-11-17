package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firefreeforest/backend/internal/repository"
)

const (
	aggregateDefaultIntervalMinutes = 5
	aggregateMaxIntervalMinutes     = 1440
	aggregateMaxRange               = 90 * 24 * time.Hour
	aggregateMaxRawRange            = 7 * 24 * time.Hour
	aggregateMaxBuckets             = 5000
	aggregateBucketTimezone         = "Asia/Jakarta"
)

type aggregateParams struct {
	from            time.Time
	to              time.Time
	intervalMinutes int
}

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

func (h *Handler) aggregateEnvironment(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	params, ok := parseAggregateParams(c)
	if !ok {
		return
	}
	if params.intervalMinutes == 0 {
		result, err := h.repo.RawEnvironment(c.Request.Context(), device.ID, params.from, params.to)
		if err != nil {
			if errors.Is(err, repository.ErrRawSourceTooLarge) {
				respondError(c, http.StatusUnprocessableEntity, "raw_source_too_large", "Data mentah melebihi 250000 baris; perkecil rentang waktu atau gunakan interval")
				return
			}
			h.log.Error("raw environment telemetry failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
			return
		}
		mode := "raw"
		if result.Decimated {
			mode = "raw_decimated"
		}
		respondData(c, http.StatusOK, gin.H{
			"device_id":        device.DeviceID,
			"from":             params.from,
			"to":               params.to,
			"interval_minutes": 0,
			"bucket_timezone":  aggregateBucketTimezone,
			"mode":             mode,
			"decimated":        result.Decimated,
			"source_points":    result.SourcePoints,
			"returned_points":  len(result.Points),
			"points":           result.Points,
		})
		return
	}
	rows, err := h.repo.AggregateEnvironment(c.Request.Context(), device.ID, params.from, params.to, params.intervalMinutes)
	if err != nil {
		h.log.Error("aggregate environment telemetry failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, gin.H{
		"device_id":        device.DeviceID,
		"from":             params.from,
		"to":               params.to,
		"interval_minutes": params.intervalMinutes,
		"bucket_timezone":  aggregateBucketTimezone,
		"points":           rows,
	})
}

func (h *Handler) aggregateFire(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	params, ok := parseAggregateParams(c)
	if !ok {
		return
	}
	if params.intervalMinutes == 0 {
		result, err := h.repo.RawFire(c.Request.Context(), device.ID, params.from, params.to)
		if err != nil {
			if errors.Is(err, repository.ErrRawSourceTooLarge) {
				respondError(c, http.StatusUnprocessableEntity, "raw_source_too_large", "Data mentah melebihi 250000 baris; perkecil rentang waktu atau gunakan interval")
				return
			}
			h.log.Error("raw fire telemetry failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
			return
		}
		mode := "raw"
		if result.Decimated {
			mode = "raw_decimated"
		}
		respondData(c, http.StatusOK, gin.H{
			"device_id":        device.DeviceID,
			"from":             params.from,
			"to":               params.to,
			"interval_minutes": 0,
			"bucket_timezone":  aggregateBucketTimezone,
			"mode":             mode,
			"decimated":        result.Decimated,
			"source_points":    result.SourcePoints,
			"returned_points":  len(result.Points),
			"points":           result.Points,
		})
		return
	}
	rows, err := h.repo.AggregateFire(c.Request.Context(), device.ID, params.from, params.to, params.intervalMinutes)
	if err != nil {
		h.log.Error("aggregate fire telemetry failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, gin.H{
		"device_id":        device.DeviceID,
		"from":             params.from,
		"to":               params.to,
		"interval_minutes": params.intervalMinutes,
		"bucket_timezone":  aggregateBucketTimezone,
		"points":           rows,
	})
}

func parseAggregateParams(c *gin.Context) (aggregateParams, bool) {
	from, err := parseTimeQuery(c, "from")
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_time", "Format waktu 'from' harus RFC3339")
		return aggregateParams{}, false
	}
	to, err := parseTimeQuery(c, "to")
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid_time", "Format waktu 'to' harus RFC3339")
		return aggregateParams{}, false
	}
	if from == nil || to == nil {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "from dan to wajib diisi")
		return aggregateParams{}, false
	}
	if !from.Before(*to) {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "from harus lebih awal dari to")
		return aggregateParams{}, false
	}

	interval := aggregateDefaultIntervalMinutes
	if raw := strings.TrimSpace(c.Query("interval")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > aggregateMaxIntervalMinutes {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", "interval harus 0 (raw) atau antara 1 dan 1440 menit")
			return aggregateParams{}, false
		}
		interval = n
	}

	span := to.Sub(*from)
	if interval == 0 {
		if span > aggregateMaxRawRange {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", "Rentang waktu raw maksimal 7 hari")
			return aggregateParams{}, false
		}
		return aggregateParams{from: *from, to: *to, intervalMinutes: 0}, true
	}
	if span > aggregateMaxRange {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "Rentang waktu maksimal 90 hari")
		return aggregateParams{}, false
	}
	step := time.Duration(interval) * time.Minute
	buckets := int((span + step - 1) / step)
	if buckets > aggregateMaxBuckets {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "Jumlah bucket maksimal 5000; perbesar interval")
		return aggregateParams{}, false
	}

	return aggregateParams{from: *from, to: *to, intervalMinutes: interval}, true
}
