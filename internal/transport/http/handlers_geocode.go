package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *Handler) reverseGeocode(c *gin.Context) {
	rawLat := strings.TrimSpace(c.Query("lat"))
	rawLon := strings.TrimSpace(c.Query("lon"))
	if rawLat == "" || rawLon == "" {
		respondError(c, http.StatusBadRequest, "invalid_request", "lat dan lon wajib diisi")
		return
	}
	latitude, err := strconv.ParseFloat(rawLat, 64)
	if err != nil || latitude < -90 || latitude > 90 {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "lat harus antara -90 dan 90")
		return
	}
	longitude, err := strconv.ParseFloat(rawLon, 64)
	if err != nil || longitude < -180 || longitude > 180 {
		respondError(c, http.StatusUnprocessableEntity, "validation_error", "lon harus antara -180 dan 180")
		return
	}
	if h.geocoder == nil {
		respondError(c, http.StatusServiceUnavailable, "geocode_unavailable", "Layanan nama lokasi belum dikonfigurasi")
		return
	}

	result, err := h.geocoder.Reverse(c.Request.Context(), latitude, longitude)
	if err != nil {
		h.log.Warn("reverse geocode failed", "lat", latitude, "lon", longitude, "error", err)
		respondError(c, http.StatusBadGateway, "geocode_unavailable", "Nama lokasi tidak tersedia; isi secara manual")
		return
	}
	respondData(c, http.StatusOK, result)
}
