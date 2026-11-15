package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

func (h *Handler) lookupDevice(c *gin.Context) (*domain.Device, bool) {
	device, err := h.repo.GetDeviceByDeviceID(c.Request.Context(), c.Param("deviceID"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Device tidak ditemukan")
		} else {
			h.log.Error("lookup device failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		}
		return nil, false
	}
	return device, true
}

func (h *Handler) listDevices(c *gin.Context) {
	page, perPage, limit, offset := pageParams(c)
	devices, total, err := h.repo.ListDevices(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("list devices failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, devices, page, perPage, total)
}

func (h *Handler) getDevice(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	respondData(c, http.StatusOK, device)
}

func (h *Handler) createDevice(c *gin.Context) {
	actor, _, _ := currentUser(c)
	var req struct {
		DeviceID        string   `json:"device_id"`
		Name            string   `json:"name"`
		LocationName    string   `json:"location_name"`
		Latitude        *float64 `json:"latitude"`
		Longitude       *float64 `json:"longitude"`
		FirmwareVersion string   `json:"firmware_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Payload device tidak valid")
		return
	}
	requestedDeviceID := strings.TrimSpace(req.DeviceID)
	if requestedDeviceID != "" && !deviceIDPattern.MatchString(requestedDeviceID) {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "device_id harus 3-64 karakter (huruf, angka, titik, garis bawah, atau strip)")
		return
	}
	if req.Latitude != nil && (*req.Latitude < -90 || *req.Latitude > 90) {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "latitude harus antara -90 dan 90")
		return
	}
	if req.Longitude != nil && (*req.Longitude < -180 || *req.Longitude > 180) {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "longitude harus antara -180 dan 180")
		return
	}

	for attempt := 0; attempt < 5; attempt++ {
		deviceID := requestedDeviceID
		if deviceID == "" {
			generated, err := generateDeviceID()
			if err != nil {
				h.log.Error("generate device id failed", "error", err)
				respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
				return
			}
			deviceID = generated
		}

		now := time.Now().UTC()
		device := &domain.Device{
			ID:              uuid.New(),
			DeviceID:        deviceID,
			Name:            strings.TrimSpace(req.Name),
			LocationName:    strings.TrimSpace(req.LocationName),
			Latitude:        req.Latitude,
			Longitude:       req.Longitude,
			Status:          domain.DeviceOffline,
			FirmwareVersion: req.FirmwareVersion,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if device.Name == "" {
			device.Name = device.DeviceID
		}
		if err := h.repo.CreateDevice(c.Request.Context(), device); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				if requestedDeviceID == "" {
					continue
				}
				respondError(c, http.StatusConflict, "duplicate_device", "device_id sudah terdaftar")
				return
			}
			h.log.Error("create device failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
			return
		}
		h.audit(c, &actor, "device.create", "device", device.DeviceID, map[string]any{"name": device.Name})
		respondData(c, http.StatusCreated, device)
		return
	}

	respondError(c, http.StatusInternalServerError, "internal_error", "Gagal membuat device_id unik")
}

func (h *Handler) updateDevice(c *gin.Context) {
	actor, _, _ := currentUser(c)
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	var req struct {
		Name            *string  `json:"name"`
		LocationName    *string  `json:"location_name"`
		Latitude        *float64 `json:"latitude"`
		Longitude       *float64 `json:"longitude"`
		FirmwareVersion *string  `json:"firmware_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Payload device tidak valid")
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		fields["name"] = strings.TrimSpace(*req.Name)
	}
	if req.LocationName != nil {
		fields["location_name"] = strings.TrimSpace(*req.LocationName)
	}
	if req.Latitude != nil {
		if *req.Latitude < -90 || *req.Latitude > 90 {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", "latitude harus antara -90 dan 90")
			return
		}
		fields["latitude"] = *req.Latitude
	}
	if req.Longitude != nil {
		if *req.Longitude < -180 || *req.Longitude > 180 {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", "longitude harus antara -180 dan 180")
			return
		}
		fields["longitude"] = *req.Longitude
	}
	if req.FirmwareVersion != nil {
		fields["firmware_version"] = *req.FirmwareVersion
	}
	if len(fields) == 0 {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "Tidak ada field yang diubah")
		return
	}
	if err := h.repo.UpdateDeviceFields(c.Request.Context(), device.ID, fields); err != nil {
		h.log.Error("update device failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	h.audit(c, &actor, "device.update", "device", device.DeviceID, fields)
	updated, err := h.repo.GetDeviceByID(c.Request.Context(), device.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, updated)
}

func (h *Handler) deleteDevice(c *gin.Context) {
	actor, _, _ := currentUser(c)
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	if err := h.repo.DeleteDevice(c.Request.Context(), device.ID); err != nil {
		h.log.Error("delete device failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	h.audit(c, &actor, "device.delete", "device", device.DeviceID, nil)
	c.Status(http.StatusNoContent)
}

func (h *Handler) latestTelemetry(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	env, fire, risk := h.cachedLatest(c, device)
	respondData(c, http.StatusOK, gin.H{
		"device":      device,
		"environment": env,
		"fire":        fire,
		"risk_level":  risk,
	})
}

func (h *Handler) deviceStatus(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	env, fire, risk := h.cachedLatest(c, device)
	respondData(c, http.StatusOK, gin.H{
		"device":       device,
		"status":       device.Status,
		"last_seen_at": device.LastSeenAt,
		"environment":  env,
		"fire":         fire,
		"risk_level":   risk,
		"server_time":  time.Now().UTC(),
	})
}

func (h *Handler) cachedLatest(c *gin.Context, device *domain.Device) (*domain.TelemetryEnvironment, *domain.TelemetryFire, string) {
	ctx := c.Request.Context()
	var env *domain.TelemetryEnvironment
	var fire *domain.TelemetryFire

	if vals, err := h.rdb.HGetAll(ctx, "ff:latest:"+device.DeviceID).Result(); err == nil {
		if raw, ok := vals["environment"]; ok && raw != "" {
			var e domain.TelemetryEnvironment
			if json.Unmarshal([]byte(raw), &e) == nil {
				env = &e
			}
		}
		if raw, ok := vals["fire"]; ok && raw != "" {
			var f domain.TelemetryFire
			if json.Unmarshal([]byte(raw), &f) == nil {
				fire = &f
			}
		}
	}
	if env == nil {
		if e, err := h.repo.LatestEnvironment(ctx, device.ID); err == nil {
			env = e
		}
	}
	if fire == nil {
		if f, err := h.repo.LatestFire(ctx, device.ID); err == nil {
			fire = f
		}
	}
	risk, err := h.rdb.Get(ctx, "ff:risk:"+device.DeviceID).Result()
	if err != nil {
		risk = "UNKNOWN"
	}
	return env, fire, risk
}
