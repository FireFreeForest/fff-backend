package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	geocodeProvider       = "openstreetmap"
	geocodeCachePrefix    = "ff:geocode:reverse:"
	geocodeEmptyCacheTTL  = 24 * time.Hour
	geocodeRequestTimeout = 6 * time.Second
	geocodeMaxLabelRunes  = 120
)

type GeocodeResult struct {
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Provider  string  `json:"provider"`
}

type Geocoder struct {
	rdb         *goredis.Client
	client      *http.Client
	baseURL     string
	userAgent   string
	cacheTTL    time.Duration
	log         *slog.Logger
	mu          sync.Mutex
	nextRequest time.Time
}

func NewGeocoder(rdb *goredis.Client, baseURL, userAgent string, cacheTTL time.Duration, log *slog.Logger) *Geocoder {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://nominatim.openstreetmap.org"
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "FireFreeForest/1.0"
	}
	if cacheTTL <= 0 {
		cacheTTL = 30 * 24 * time.Hour
	}
	return &Geocoder{
		rdb:       rdb,
		client:    &http.Client{Timeout: geocodeRequestTimeout},
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
		cacheTTL:  cacheTTL,
		log:       log,
	}
}

func (g *Geocoder) Reverse(ctx context.Context, latitude, longitude float64) (*GeocodeResult, error) {
	key := fmt.Sprintf("%s%.4f:%.4f", geocodeCachePrefix, latitude, longitude)
	if raw, err := g.rdb.Get(ctx, key).Result(); err == nil && raw != "" {
		var cached GeocodeResult
		if json.Unmarshal([]byte(raw), &cached) == nil {
			return &cached, nil
		}
	}

	if err := g.waitForSlot(ctx); err != nil {
		return nil, err
	}

	endpoint, err := url.Parse(g.baseURL + "/reverse")
	if err != nil {
		return nil, fmt.Errorf("parse geocode endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("format", "jsonv2")
	query.Set("lat", strconv.FormatFloat(latitude, 'f', 6, 64))
	query.Set("lon", strconv.FormatFloat(longitude, 'f', 6, 64))
	query.Set("zoom", "16")
	query.Set("addressdetails", "1")
	query.Set("accept-language", "id")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build geocode request: %w", err)
	}
	req.Header.Set("User-Agent", g.userAgent)
	req.Header.Set("Accept", "application/json")

	response, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geocode request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("geocode upstream status %d", response.StatusCode)
	}

	var payload struct {
		DisplayName string            `json:"display_name"`
		Name        string            `json:"name"`
		Address     map[string]string `json:"address"`
		Error       string            `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode geocode response: %w", err)
	}
	if strings.TrimSpace(payload.Error) != "" {
		return nil, fmt.Errorf("geocode provider error: %s", payload.Error)
	}

	result := &GeocodeResult{
		Label:     buildGeocodeLabel(payload.DisplayName, payload.Name, payload.Address),
		Latitude:  latitude,
		Longitude: longitude,
		Provider:  geocodeProvider,
	}

	ttl := g.cacheTTL
	if result.Label == "" {
		ttl = geocodeEmptyCacheTTL
	}
	if raw, err := json.Marshal(result); err == nil {
		if err := g.rdb.Set(ctx, key, raw, ttl).Err(); err != nil && g.log != nil {
			g.log.Warn("failed to cache reverse geocode", "error", err)
		}
	}
	return result, nil
}

func (g *Geocoder) waitForSlot(ctx context.Context) error {
	for {
		g.mu.Lock()
		now := time.Now()
		if !g.nextRequest.After(now) {
			g.nextRequest = now.Add(time.Second)
			g.mu.Unlock()
			return nil
		}
		wait := g.nextRequest.Sub(now)
		g.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func buildGeocodeLabel(displayName, name string, address map[string]string) string {
	keys := []string{
		"road", "neighbourhood", "suburb", "village", "hamlet",
		"town", "city", "municipality", "county", "state",
	}
	parts := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(parts) >= 4 {
			return
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, value)
	}

	add(name)
	for _, key := range keys {
		add(address[key])
	}
	if len(parts) == 0 {
		for _, part := range strings.Split(displayName, ",") {
			add(part)
			if len(parts) >= 4 {
				break
			}
		}
	}

	label := strings.Join(parts, ", ")
	runes := []rune(label)
	if len(runes) > geocodeMaxLabelRunes {
		label = strings.TrimSpace(string(runes[:geocodeMaxLabelRunes])) + "…"
	}
	return label
}
