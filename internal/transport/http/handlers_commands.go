package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

func (h *Handler) issueCommand(c *gin.Context) {
	userID, _, _ := currentUser(c)
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}

	var req struct {
		Action  string `json:"action" binding:"required"`
		Payload *struct {
			State    *bool `json:"state"`
			Duration *int  `json:"duration"`
		} `json:"payload"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "action wajib diisi")
		return
	}

	action := domain.CommandAction(strings.ToUpper(strings.TrimSpace(req.Action)))
	payload := map[string]any{}
	switch action {
	case domain.ActionSetPump:
		if req.Payload == nil || req.Payload.State == nil {
			respondError(c, http.StatusBadRequest, "invalid_request", "action dan payload.state wajib diisi")
			return
		}
		duration := int(h.cfg.AutoPumpDuration.Seconds())
		if req.Payload.Duration != nil {
			duration = *req.Payload.Duration
		}
		if !*req.Payload.State {
			duration = 0
		}
		maxDuration := int(h.cfg.AutoPumpMax.Seconds())
		if *req.Payload.State && (duration < 1 || duration > maxDuration) {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", fmt.Sprintf("duration harus antara 1 dan %d detik", maxDuration))
			return
		}
		payload["state"] = *req.Payload.State
		payload["duration"] = duration
	case domain.ActionGetAllTelemetry:
		// No parameters: asks the device to publish every telemetry stream now.
	default:
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "Action yang didukung hanya SET_PUMP dan GET_ALL_TELEMETRY")
		return
	}

	ctx := c.Request.Context()
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	redisKey := ""
	if idempotencyKey != "" {
		redisKey = fmt.Sprintf("ff:idem:%s:%s", userID.String(), idempotencyKey)
		acquired, err := h.rdb.SetNX(ctx, redisKey, "pending", 24*time.Hour).Result()
		if err != nil {
			h.log.Warn("idempotency check failed", "error", err)
		} else if !acquired {
			existing, _ := h.rdb.Get(ctx, redisKey).Result()
			if existing != "" && existing != "pending" {
				cmd, err := h.repo.GetCommandByCommandID(ctx, existing)
				if err == nil {
					respondData(c, http.StatusOK, cmd)
					return
				}
			}
			respondError(c, http.StatusConflict, "idempotency_in_progress", "Permintaan dengan Idempotency-Key yang sama sedang diproses")
			return
		}
	}

	cmd, err := h.commands.Issue(ctx, device, service.IssueCommandRequest{
		Action:   action,
		Payload:  payload,
		Origin:   domain.OriginManual,
		IssuedBy: &userID,
	})
	if err != nil {
		if redisKey != "" {
			h.rdb.Del(ctx, redisKey)
		}
		if errors.Is(err, service.ErrValidation) {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		h.log.Error("issue command failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	if redisKey != "" {
		h.rdb.Set(ctx, redisKey, cmd.CommandID, 24*time.Hour)
	}
	h.audit(c, &userID, "command.issue", "command", cmd.CommandID, map[string]any{
		"device_id": device.DeviceID,
		"action":    cmd.Action,
	})
	respondData(c, http.StatusAccepted, cmd)
}

func (h *Handler) listCommands(c *gin.Context) {
	device, ok := h.lookupDevice(c)
	if !ok {
		return
	}
	page, perPage, limit, offset := pageParams(c)
	rows, total, err := h.repo.ListCommands(c.Request.Context(), device.ID, limit, offset)
	if err != nil {
		h.log.Error("list commands failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, rows, page, perPage, total)
}

func (h *Handler) getCommand(c *gin.Context) {
	commandID := c.Param("commandID")
	cmd, err := h.repo.GetCommandByCommandID(c.Request.Context(), commandID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "Command tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, cmd)
}
