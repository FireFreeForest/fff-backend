package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

type tools struct {
	deps Deps
	role domain.UserRole
}

// ---------------------------------------------------------------- inputs

type listDevicesInput struct {
	Query   string `json:"query,omitempty" jsonschema:"Teks pencarian pada nama, lokasi, atau device_id"`
	Page    int    `json:"page,omitempty" jsonschema:"Halaman data, dimulai dari 1 (default 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Jumlah item per halaman, 1-100 (default 20)"`
}

type deviceInput struct {
	DeviceID string `json:"device_id" jsonschema:"device_id publik perangkat, contoh: fff-001"`
}

type telemetryListInput struct {
	DeviceID string `json:"device_id" jsonschema:"device_id publik perangkat"`
	From     string `json:"from,omitempty" jsonschema:"Awal rentang waktu dalam RFC3339 UTC"`
	To       string `json:"to,omitempty" jsonschema:"Akhir rentang waktu dalam RFC3339 UTC"`
	Page     int    `json:"page,omitempty" jsonschema:"Halaman data (default 1)"`
	PerPage  int    `json:"per_page,omitempty" jsonschema:"Jumlah item per halaman, 1-100 (default 20)"`
}

type telemetryAggregateInput struct {
	DeviceID        string `json:"device_id" jsonschema:"device_id publik perangkat"`
	From            string `json:"from" jsonschema:"Awal rentang waktu dalam RFC3339 UTC (wajib)"`
	To              string `json:"to" jsonschema:"Akhir rentang waktu dalam RFC3339 UTC (wajib)"`
	IntervalMinutes *int   `json:"interval_minutes,omitempty" jsonschema:"Ukuran bucket dalam menit, 0 untuk data mentah, 1-1440 (default 5)"`
}

type listEventsInput struct {
	DeviceID  string `json:"device_id,omitempty" jsonschema:"Filter device_id publik"`
	EventType string `json:"event_type,omitempty" jsonschema:"Filter tipe event, contoh: FIRE_DETECTED"`
	Severity  string `json:"severity,omitempty" jsonschema:"Filter severity: error, warning, atau critical"`
	Status    string `json:"status,omitempty" jsonschema:"Filter status: active, acknowledged, atau resolved"`
	From      string `json:"from,omitempty" jsonschema:"Awal rentang triggered_at dalam RFC3339 UTC"`
	To        string `json:"to,omitempty" jsonschema:"Akhir rentang triggered_at dalam RFC3339 UTC"`
	Page      int    `json:"page,omitempty" jsonschema:"Halaman data (default 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Jumlah item per halaman, 1-100 (default 20)"`
}

type eventInput struct {
	EventID string `json:"event_id" jsonschema:"UUID event"`
}

type eventTimelineInput struct {
	EventID string `json:"event_id" jsonschema:"UUID event"`
	Include string `json:"include,omitempty" jsonschema:"all atau important (default all)"`
	Page    int    `json:"page,omitempty" jsonschema:"Halaman data (default 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Jumlah item per halaman, 1-100 (default 20)"`
}

type listCommandsInput struct {
	DeviceID string `json:"device_id" jsonschema:"device_id publik perangkat"`
	Page     int    `json:"page,omitempty" jsonschema:"Halaman data (default 1)"`
	PerPage  int    `json:"per_page,omitempty" jsonschema:"Jumlah item per halaman, 1-100 (default 20)"`
}

type commandInput struct {
	CommandID string `json:"command_id" jsonschema:"command_id publik, contoh: cmd-20260101-1234"`
}

type geocodeInput struct {
	Latitude  float64 `json:"latitude" jsonschema:"Lintang antara -90 dan 90"`
	Longitude float64 `json:"longitude" jsonschema:"Bujur antara -180 dan 180"`
}

type emptyInput struct{}

// ---------------------------------------------------------------- outputs

type deviceStatusOutput struct {
	Device        *domain.Device               `json:"device"`
	Status        domain.DeviceStatus          `json:"status"`
	LastSeenAt    *time.Time                   `json:"last_seen_at,omitempty"`
	OnlineSince   *time.Time                   `json:"online_since,omitempty"`
	UptimeSeconds int64                        `json:"uptime_seconds"`
	Environment   *domain.TelemetryEnvironment `json:"environment,omitempty"`
	Fire          *domain.TelemetryFire        `json:"fire,omitempty"`
	RiskLevel     string                       `json:"risk_level"`
	ServerTime    time.Time                    `json:"server_time"`
}

type latestTelemetryOutput struct {
	Device      *domain.Device               `json:"device"`
	Environment *domain.TelemetryEnvironment `json:"environment,omitempty"`
	Fire        *domain.TelemetryFire        `json:"fire,omitempty"`
	RiskLevel   string                       `json:"risk_level"`
}

type aggregateOutput struct {
	DeviceID        string    `json:"device_id"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	IntervalMinutes int       `json:"interval_minutes"`
	BucketTimezone  string    `json:"bucket_timezone"`
	Mode            string    `json:"mode,omitempty"`
	Decimated       bool      `json:"decimated,omitempty"`
	SourcePoints    int       `json:"source_points,omitempty"`
	ReturnedPoints  int       `json:"returned_points,omitempty"`
	Points          any       `json:"points"`
}

type dashboardOutput struct {
	Devices      dashboardDevices     `json:"devices"`
	Risk         dashboardRisk        `json:"risk"`
	ActiveEvents dashboardActiveEvent `json:"active_events"`
	RecentEvents []domain.Event       `json:"recent_events"`
}

type dashboardDevices struct {
	Total   int64 `json:"total"`
	Online  int64 `json:"online"`
	Offline int64 `json:"offline"`
}

type dashboardRisk struct {
	Counts map[string]int64 `json:"counts"`
}

type dashboardActiveEvent struct {
	Total      int64            `json:"total"`
	BySeverity map[string]int64 `json:"by_severity"`
}

// ---------------------------------------------------------------- registration

func registerReadTools(s *sdkmcp.Server, d Deps, role domain.UserRole) {
	t := &tools{deps: d, role: role}

	sdkmcp.AddTool(s, readTool("list_devices",
		"Daftar perangkat dengan pencarian dan pagination. Gunakan untuk menemukan device_id sebelum meminta detail atau telemetri.",
		"Daftar Perangkat"), instrument(role, "list_devices", t.listDevices))

	sdkmcp.AddTool(s, readTool("get_device",
		"Detail satu perangkat berdasarkan device_id publik.",
		"Detail Perangkat"), instrument(role, "get_device", t.getDevice))

	sdkmcp.AddTool(s, readTool("get_device_status",
		"Status online/offline, uptime, telemetri terakhir, dan risk level satu perangkat.",
		"Status Perangkat"), instrument(role, "get_device_status", t.getDeviceStatus))

	sdkmcp.AddTool(s, readTool("get_latest_telemetry",
		"Telemetri lingkungan dan kebakaran terbaru beserta risk level satu perangkat.",
		"Telemetri Terbaru"), instrument(role, "get_latest_telemetry", t.getLatestTelemetry))

	sdkmcp.AddTool(s, readTool("list_environment_telemetry",
		"Riwayat telemetri lingkungan (suhu, kelembapan, kelembapan tanah) dengan filter waktu dan pagination.",
		"Riwayat Telemetri Lingkungan"), instrument(role, "list_environment_telemetry", t.listEnvironmentTelemetry))

	sdkmcp.AddTool(s, readTool("aggregate_environment_telemetry",
		"Agregasi telemetri lingkungan per bucket waktu. interval_minutes=0 mengembalikan data mentah (maksimal 7 hari).",
		"Agregasi Telemetri Lingkungan"), instrument(role, "aggregate_environment_telemetry", t.aggregateEnvironmentTelemetry))

	sdkmcp.AddTool(s, readTool("list_fire_telemetry",
		"Riwayat telemetri kebakaran (flame, smoke, pump, buzzer) dengan filter waktu dan pagination.",
		"Riwayat Telemetri Kebakaran"), instrument(role, "list_fire_telemetry", t.listFireTelemetry))

	sdkmcp.AddTool(s, readTool("aggregate_fire_telemetry",
		"Agregasi telemetri kebakaran per bucket waktu. interval_minutes=0 mengembalikan data mentah (maksimal 7 hari).",
		"Agregasi Telemetri Kebakaran"), instrument(role, "aggregate_fire_telemetry", t.aggregateFireTelemetry))

	sdkmcp.AddTool(s, readTool("list_events",
		"Daftar event dengan filter device, tipe, severity, status, dan rentang waktu.",
		"Daftar Event"), instrument(role, "list_events", t.listEvents))

	sdkmcp.AddTool(s, readTool("get_event",
		"Detail event beserta preview timeline aktivitas terbaru.",
		"Detail Event"), instrument(role, "get_event", t.getEvent))

	sdkmcp.AddTool(s, readTool("list_event_timeline",
		"Timeline lengkap satu event (notifikasi, acknowledgement, resolusi, dan aktivitas lain).",
		"Timeline Event"), instrument(role, "list_event_timeline", t.listEventTimeline))

	sdkmcp.AddTool(s, readTool("list_commands",
		"Daftar command yang pernah dikirim ke satu perangkat.",
		"Daftar Command"), instrument(role, "list_commands", t.listCommands))

	sdkmcp.AddTool(s, readTool("get_command",
		"Detail satu command berdasarkan command_id publik.",
		"Detail Command"), instrument(role, "get_command", t.getCommand))

	sdkmcp.AddTool(s, readTool("get_dashboard_summary",
		"Ringkasan dashboard: jumlah perangkat online/offline, distribusi risk, event aktif per severity, dan event terbaru.",
		"Ringkasan Dashboard"), instrument(role, "get_dashboard_summary", t.getDashboardSummary))

	sdkmcp.AddTool(s, readTool("reverse_geocode",
		"Ubah koordinat menjadi nama lokasi menggunakan penyedia geocoding yang dikonfigurasi.",
		"Reverse Geocode"), instrument(role, "reverse_geocode", t.reverseGeocode))
}

// ---------------------------------------------------------------- read handlers

func (t *tools) listDevices(ctx context.Context, _ *sdkmcp.CallToolRequest, in listDevicesInput) (*sdkmcp.CallToolResult, any, error) {
	query := strings.TrimSpace(in.Query)
	if len([]rune(query)) > maxSearchQueryLength {
		return nil, nil, fmt.Errorf("query maksimal %d karakter", maxSearchQueryLength)
	}
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	rows, total, err := t.deps.Repo.ListDevices(ctx, repository.DeviceFilter{
		Query:  query,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(rows, page, perPage, total))
}

func (t *tools) getDevice(ctx context.Context, _ *sdkmcp.CallToolRequest, in deviceInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(device)
}

func (t *tools) getDeviceStatus(ctx context.Context, _ *sdkmcp.CallToolRequest, in deviceInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	latest := latestTelemetry(ctx, t.deps.Redis, t.deps.Repo, device)
	now := time.Now().UTC()
	return jsonResult(deviceStatusOutput{
		Device:        device,
		Status:        device.Status,
		LastSeenAt:    device.LastSeenAt,
		OnlineSince:   device.OnlineSince,
		UptimeSeconds: device.UptimeSeconds(now),
		Environment:   latest.Environment,
		Fire:          latest.Fire,
		RiskLevel:     latest.RiskLevel,
		ServerTime:    now,
	})
}

func (t *tools) getLatestTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in deviceInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	latest := latestTelemetry(ctx, t.deps.Redis, t.deps.Repo, device)
	return jsonResult(latestTelemetryOutput{
		Device:      device,
		Environment: latest.Environment,
		Fire:        latest.Fire,
		RiskLevel:   latest.RiskLevel,
	})
}

func (t *tools) listEnvironmentTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in telemetryListInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	from, err := parseOptionalTime(in.From, "from")
	if err != nil {
		return nil, nil, err
	}
	to, err := parseOptionalTime(in.To, "to")
	if err != nil {
		return nil, nil, err
	}
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	rows, total, err := t.deps.Repo.ListEnvironment(ctx, device.ID, from, to, limit, offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(rows, page, perPage, total))
}

func (t *tools) aggregateEnvironmentTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in telemetryAggregateInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	from, to, interval, err := parseAggregate(in.From, in.To, in.IntervalMinutes)
	if err != nil {
		return nil, nil, err
	}
	if interval == 0 {
		result, err := t.deps.Repo.RawEnvironment(ctx, device.ID, from, to)
		if err != nil {
			if errors.Is(err, repository.ErrRawSourceTooLarge) {
				return nil, nil, errors.New("data mentah melebihi 250000 baris; perkecil rentang waktu atau gunakan interval")
			}
			return nil, nil, err
		}
		mode := "raw"
		if result.Decimated {
			mode = "raw_decimated"
		}
		return jsonResult(aggregateOutput{
			DeviceID:        device.DeviceID,
			From:            from,
			To:              to,
			IntervalMinutes: 0,
			BucketTimezone:  bucketTimezone,
			Mode:            mode,
			Decimated:       result.Decimated,
			SourcePoints:    result.SourcePoints,
			ReturnedPoints:  len(result.Points),
			Points:          result.Points,
		})
	}
	rows, err := t.deps.Repo.AggregateEnvironment(ctx, device.ID, from, to, interval)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(aggregateOutput{
		DeviceID:        device.DeviceID,
		From:            from,
		To:              to,
		IntervalMinutes: interval,
		BucketTimezone:  bucketTimezone,
		Points:          rows,
	})
}

func (t *tools) listFireTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in telemetryListInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	from, err := parseOptionalTime(in.From, "from")
	if err != nil {
		return nil, nil, err
	}
	to, err := parseOptionalTime(in.To, "to")
	if err != nil {
		return nil, nil, err
	}
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	rows, total, err := t.deps.Repo.ListFire(ctx, device.ID, from, to, limit, offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(rows, page, perPage, total))
}

func (t *tools) aggregateFireTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in telemetryAggregateInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	from, to, interval, err := parseAggregate(in.From, in.To, in.IntervalMinutes)
	if err != nil {
		return nil, nil, err
	}
	if interval == 0 {
		result, err := t.deps.Repo.RawFire(ctx, device.ID, from, to)
		if err != nil {
			if errors.Is(err, repository.ErrRawSourceTooLarge) {
				return nil, nil, errors.New("data mentah melebihi 250000 baris; perkecil rentang waktu atau gunakan interval")
			}
			return nil, nil, err
		}
		mode := "raw"
		if result.Decimated {
			mode = "raw_decimated"
		}
		return jsonResult(aggregateOutput{
			DeviceID:        device.DeviceID,
			From:            from,
			To:              to,
			IntervalMinutes: 0,
			BucketTimezone:  bucketTimezone,
			Mode:            mode,
			Decimated:       result.Decimated,
			SourcePoints:    result.SourcePoints,
			ReturnedPoints:  len(result.Points),
			Points:          result.Points,
		})
	}
	rows, err := t.deps.Repo.AggregateFire(ctx, device.ID, from, to, interval)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(aggregateOutput{
		DeviceID:        device.DeviceID,
		From:            from,
		To:              to,
		IntervalMinutes: interval,
		BucketTimezone:  bucketTimezone,
		Points:          rows,
	})
}

func (t *tools) listEvents(ctx context.Context, _ *sdkmcp.CallToolRequest, in listEventsInput) (*sdkmcp.CallToolResult, any, error) {
	filter := repository.EventFilter{
		EventType: strings.ToUpper(strings.TrimSpace(in.EventType)),
		Severity:  strings.ToLower(strings.TrimSpace(in.Severity)),
		Status:    strings.ToLower(strings.TrimSpace(in.Status)),
	}
	if raw := strings.TrimSpace(in.DeviceID); raw != "" {
		device, err := t.deps.Repo.GetDeviceByDeviceID(ctx, raw)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, nil, fmt.Errorf("device %q tidak ditemukan", raw)
			}
			return nil, nil, err
		}
		filter.DeviceID = &device.ID
	}
	from, err := parseOptionalTime(in.From, "from")
	if err != nil {
		return nil, nil, err
	}
	to, err := parseOptionalTime(in.To, "to")
	if err != nil {
		return nil, nil, err
	}
	filter.From = from
	filter.To = to
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	filter.Limit = limit
	filter.Offset = offset
	rows, total, err := t.deps.Repo.ListEvents(ctx, filter)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(rows, page, perPage, total))
}

func (t *tools) getEvent(ctx context.Context, _ *sdkmcp.CallToolRequest, in eventInput) (*sdkmcp.CallToolResult, any, error) {
	id, err := parseUUIDField(in.EventID, "event_id")
	if err != nil {
		return nil, nil, err
	}
	event, err := t.deps.Repo.GetEvent(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, fmt.Errorf("event %s tidak ditemukan", in.EventID)
		}
		return nil, nil, err
	}
	preview, err := t.deps.Repo.EventTimelinePreview(ctx, id, 5)
	if err != nil {
		return nil, nil, err
	}
	total, err := t.deps.Repo.EventTimelineCount(ctx, id, "all")
	if err != nil {
		return nil, nil, err
	}
	items := newTimelineItems(preview)
	return jsonResult(eventDetail{
		Event:           event,
		TimelinePreview: items,
		TimelineTotal:   total,
		TimelineHasMore: total > int64(len(items)),
	})
}

func (t *tools) listEventTimeline(ctx context.Context, _ *sdkmcp.CallToolRequest, in eventTimelineInput) (*sdkmcp.CallToolResult, any, error) {
	id, err := parseUUIDField(in.EventID, "event_id")
	if err != nil {
		return nil, nil, err
	}
	if _, err := t.deps.Repo.GetEvent(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, fmt.Errorf("event %s tidak ditemukan", in.EventID)
		}
		return nil, nil, err
	}
	include := strings.ToLower(strings.TrimSpace(in.Include))
	if include == "" {
		include = "all"
	}
	if include != "all" && include != "important" {
		return nil, nil, errors.New("include harus all atau important")
	}
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	rows, total, err := t.deps.Repo.ListEventTimeline(ctx, id, include, limit, offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(newTimelineItems(rows), page, perPage, total))
}

func (t *tools) listCommands(ctx context.Context, _ *sdkmcp.CallToolRequest, in listCommandsInput) (*sdkmcp.CallToolResult, any, error) {
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	page, perPage, limit, offset := pageParams(in.Page, in.PerPage)
	rows, total, err := t.deps.Repo.ListCommands(ctx, device.ID, limit, offset)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(newListResponse(rows, page, perPage, total))
}

func (t *tools) getCommand(ctx context.Context, _ *sdkmcp.CallToolRequest, in commandInput) (*sdkmcp.CallToolResult, any, error) {
	commandID := strings.TrimSpace(in.CommandID)
	if commandID == "" {
		return nil, nil, errors.New("command_id wajib diisi")
	}
	cmd, err := t.deps.Repo.GetCommandByCommandID(ctx, commandID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, fmt.Errorf("command %s tidak ditemukan", commandID)
		}
		return nil, nil, err
	}
	return jsonResult(cmd)
}

func (t *tools) getDashboardSummary(ctx context.Context, _ *sdkmcp.CallToolRequest, _ emptyInput) (*sdkmcp.CallToolResult, any, error) {
	summary, err := buildDashboard(ctx, t.deps)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(summary)
}

func buildDashboard(ctx context.Context, d Deps) (dashboardOutput, error) {
	deviceCounts, err := d.Repo.CountDevicesByStatus(ctx)
	if err != nil {
		return dashboardOutput{}, err
	}
	severityCounts, activeTotal, err := d.Repo.ActiveEventSeverityCounts(ctx)
	if err != nil {
		return dashboardOutput{}, err
	}

	riskCounts := map[string]int64{
		"NORMAL":  0,
		"WASPADA": 0,
		"BAHAYA":  0,
		"DARURAT": 0,
		"UNKNOWN": 0,
	}
	devices, _, err := d.Repo.ListDevices(ctx, repository.DeviceFilter{Limit: 1000})
	if err == nil && len(devices) > 0 && d.Redis != nil {
		keys := make([]string, 0, len(devices))
		for i := range devices {
			keys = append(keys, "ff:risk:"+devices[i].DeviceID)
		}
		if values, err := d.Redis.MGet(ctx, keys...).Result(); err == nil {
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

	recent, _, err := d.Repo.ListEvents(ctx, repository.EventFilter{Limit: 5, Offset: 0})
	if err != nil {
		recent = nil
	}

	return dashboardOutput{
		Devices: dashboardDevices{
			Total:   deviceCounts["online"] + deviceCounts["offline"],
			Online:  deviceCounts["online"],
			Offline: deviceCounts["offline"],
		},
		Risk: dashboardRisk{Counts: riskCounts},
		ActiveEvents: dashboardActiveEvent{
			Total:      activeTotal,
			BySeverity: severityCounts,
		},
		RecentEvents: recent,
	}, nil
}

func (t *tools) reverseGeocode(ctx context.Context, _ *sdkmcp.CallToolRequest, in geocodeInput) (*sdkmcp.CallToolResult, any, error) {
	if in.Latitude < -90 || in.Latitude > 90 {
		return nil, nil, errors.New("latitude harus antara -90 dan 90")
	}
	if in.Longitude < -180 || in.Longitude > 180 {
		return nil, nil, errors.New("longitude harus antara -180 dan 180")
	}
	if t.deps.Geocoder == nil {
		return nil, nil, errors.New("layanan nama lokasi belum dikonfigurasi")
	}
	result, err := t.deps.Geocoder.Reverse(ctx, in.Latitude, in.Longitude)
	if err != nil {
		return nil, nil, errors.New("nama lokasi tidak tersedia; isi secara manual")
	}
	return jsonResult(result)
}

func parseAggregate(rawFrom, rawTo string, intervalPtr *int) (time.Time, time.Time, int, error) {
	from, err := parseOptionalTime(rawFrom, "from")
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	to, err := parseOptionalTime(rawTo, "to")
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	if from == nil || to == nil {
		return time.Time{}, time.Time{}, 0, errors.New("from dan to wajib diisi")
	}
	if !from.Before(*to) {
		return time.Time{}, time.Time{}, 0, errors.New("from harus lebih awal dari to")
	}
	interval := defaultIntervalMin
	if intervalPtr != nil {
		interval = *intervalPtr
	}
	if interval < 0 || interval > maxIntervalMin {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("interval_minutes harus 0 (raw) atau antara 1 dan %d", maxIntervalMin)
	}
	span := to.Sub(*from)
	if interval == 0 {
		if span > maxRawRange {
			return time.Time{}, time.Time{}, 0, errors.New("rentang waktu raw maksimal 7 hari")
		}
		return *from, *to, 0, nil
	}
	if span > maxAggregateRange {
		return time.Time{}, time.Time{}, 0, errors.New("rentang waktu maksimal 90 hari")
	}
	step := time.Duration(interval) * time.Minute
	buckets := int((span + step - 1) / step)
	if buckets > maxAggregateBuckets {
		return time.Time{}, time.Time{}, 0, errors.New("jumlah bucket maksimal 5000; perbesar interval_minutes")
	}
	return *from, *to, interval, nil
}
