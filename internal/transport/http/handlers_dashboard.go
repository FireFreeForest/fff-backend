package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"firefreeforest/backend/internal/repository"
)

func (h *Handler) dashboardSummary(c *gin.Context) {
	ctx := c.Request.Context()

	deviceCounts, err := h.repo.CountDevicesByStatus(ctx)
	if err != nil {
		h.log.Error("dashboard device counts failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	severityCounts, activeTotal, err := h.repo.ActiveEventSeverityCounts(ctx)
	if err != nil {
		h.log.Error("dashboard event counts failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}

	riskCounts := map[string]int64{
		"NORMAL":  0,
		"WASPADA": 0,
		"BAHAYA":  0,
		"DARURAT": 0,
		"UNKNOWN": 0,
	}
	devices, _, err := h.repo.ListDevices(ctx, repository.DeviceFilter{Limit: 1000})
	if err == nil && len(devices) > 0 {
		keys := make([]string, 0, len(devices))
		for i := range devices {
			keys = append(keys, "ff:risk:"+devices[i].DeviceID)
		}
		if values, err := h.rdb.MGet(ctx, keys...).Result(); err == nil {
			for _, v := range values {
				level, ok := v.(string)
				if !ok || level == "" {
					riskCounts["UNKNOWN"]++
					continue
				}
				if _, exists := riskCounts[level]; exists {
					riskCounts[level]++
				} else {
					riskCounts["UNKNOWN"]++
				}
			}
		}
	}

	recent, _, err := h.repo.ListEvents(ctx, repository.EventFilter{Limit: 5, Offset: 0})
	if err != nil {
		recent = nil
	}

	totalDevices := deviceCounts["online"] + deviceCounts["offline"]
	respondData(c, http.StatusOK, gin.H{
		"devices": gin.H{
			"total":   totalDevices,
			"online":  deviceCounts["online"],
			"offline": deviceCounts["offline"],
		},
		"risk": gin.H{
			"counts": riskCounts,
		},
		"active_events": gin.H{
			"total":       activeTotal,
			"by_severity": severityCounts,
		},
		"recent_events": recent,
	})
}
