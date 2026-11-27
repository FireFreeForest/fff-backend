package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/observability"
	"firefreeforest/backend/internal/repository"
)

const (
	defaultPerPage       = 20
	maxPerPage           = 100
	maxSearchQueryLength = 100
	defaultIntervalMin   = 5
	maxIntervalMin       = 1440
	maxAggregateRange    = 90 * 24 * time.Hour
	maxRawRange          = 7 * 24 * time.Hour
	maxAggregateBuckets  = 5000
	bucketTimezone       = "Asia/Jakarta"
	mcpReadOnlyRole      = "readonly"
)

type pageMeta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

type listResponse struct {
	Data any      `json:"data"`
	Meta pageMeta `json:"meta"`
}

func nowUTC() time.Time { return time.Now().UTC() }

func pageParams(page, perPage int) (int, int, int, int) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	return page, perPage, perPage, (page - 1) * perPage
}

func newListResponse(data any, page, perPage int, total int64) listResponse {
	return listResponse{
		Data: data,
		Meta: pageMeta{Page: page, PerPage: perPage, Total: total},
	}
}

func parseOptionalTime(raw, field string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s harus dalam format RFC3339", field)
	}
	t = t.UTC()
	return &t, nil
}

func parseUUIDField(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s tidak valid", field)
	}
	return id, nil
}

// jsonResult serializes v as a single JSON text block. Using text keeps the
// payload readable for every MCP client and avoids output-schema coupling.
func jsonResult(v any) (*sdkmcp.CallToolResult, any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("gagal menyusun respons: %w", err)
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}},
	}, nil, nil
}

// instrument records Prometheus metrics for a tool call. It is intentionally
// generic so every handler keeps its typed input.
func instrument[In, Out any](tool string, fn sdkmcp.ToolHandlerFor[In, Out]) sdkmcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, Out, error) {
		started := time.Now()
		result, out, err := fn(ctx, req, in)
		status := "success"
		switch {
		case err != nil:
			status = "error"
		case result != nil && result.IsError:
			status = "tool_error"
		}
		observability.MCPToolCalls.WithLabelValues(tool, mcpReadOnlyRole, status).Inc()
		observability.MCPToolDuration.WithLabelValues(tool, mcpReadOnlyRole).Observe(time.Since(started).Seconds())
		return result, out, err
	}
}

func readTool(name, description, title string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:        name,
		Description: description,
		Title:       title,
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

func lookupDevice(ctx context.Context, repo *repository.Repository, deviceID string) (*domain.Device, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, errors.New("device_id wajib diisi")
	}
	device, err := repo.GetDeviceByDeviceID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("device %q tidak ditemukan", deviceID)
		}
		return nil, err
	}
	return device, nil
}

type latestTelemetryResult struct {
	Environment     *domain.TelemetryEnvironment
	Fire            *domain.TelemetryFire
	DeviceTelemetry *domain.TelemetryDevice
	RiskLevel       string
}

func latestTelemetry(ctx context.Context, rdb *goredis.Client, repo *repository.Repository, device *domain.Device) latestTelemetryResult {
	var out latestTelemetryResult
	if rdb != nil {
		if vals, err := rdb.HGetAll(ctx, "ff:latest:"+device.DeviceID).Result(); err == nil {
			if raw, ok := vals["environment"]; ok && raw != "" {
				var env domain.TelemetryEnvironment
				if json.Unmarshal([]byte(raw), &env) == nil {
					out.Environment = &env
				}
			}
			if raw, ok := vals["fire"]; ok && raw != "" {
				var fire domain.TelemetryFire
				if json.Unmarshal([]byte(raw), &fire) == nil {
					out.Fire = &fire
				}
			}
			if raw, ok := vals["device"]; ok && raw != "" {
				var dev domain.TelemetryDevice
				if json.Unmarshal([]byte(raw), &dev) == nil {
					out.DeviceTelemetry = &dev
				}
			}
		}
	}
	if out.Environment == nil {
		if env, err := repo.LatestEnvironment(ctx, device.ID); err == nil {
			out.Environment = env
		}
	}
	if out.Fire == nil {
		if fire, err := repo.LatestFire(ctx, device.ID); err == nil {
			out.Fire = fire
		}
	}
	if out.DeviceTelemetry == nil {
		if dev, err := repo.LatestDeviceTelemetry(ctx, device.ID); err == nil {
			out.DeviceTelemetry = dev
		}
	}
	out.RiskLevel = "UNKNOWN"
	if rdb != nil {
		if risk, err := rdb.Get(ctx, "ff:risk:"+device.DeviceID).Result(); err == nil && risk != "" {
			out.RiskLevel = risk
		}
	}
	return out
}

// ---------------------------------------------------------------- events

type timelineActor struct {
	Type string          `json:"type"`
	ID   *uuid.UUID      `json:"id,omitempty"`
	Name string          `json:"name"`
	Role domain.UserRole `json:"role,omitempty"`
}

type timelineItem struct {
	ID          uint64                       `json:"id"`
	EventID     uuid.UUID                    `json:"event_id"`
	Kind        domain.EventTimelineKind     `json:"kind"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	Outcome     *domain.EventTimelineOutcome `json:"outcome,omitempty"`
	Level       domain.EventTimelineLevel    `json:"level"`
	Actor       timelineActor                `json:"actor"`
	Metadata    json.RawMessage              `json:"metadata,omitempty"`
	OccurredAt  time.Time                    `json:"occurred_at"`
}

func newTimelineItems(rows []domain.EventTimeline) []timelineItem {
	items := make([]timelineItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, timelineItem{
			ID:          row.ID,
			EventID:     row.EventID,
			Kind:        row.Kind,
			Title:       row.Title,
			Description: row.Message,
			Outcome:     row.Outcome,
			Level:       row.Level,
			Actor: timelineActor{
				Type: string(row.ActorType),
				ID:   row.ActorID,
				Name: row.ActorName,
				Role: row.ActorRole,
			},
			Metadata:   row.Metadata,
			OccurredAt: row.OccurredAt,
		})
	}
	return items
}

type eventDetail struct {
	*domain.Event
	TimelinePreview []timelineItem `json:"timeline_preview"`
	TimelineTotal   int64          `json:"timeline_total"`
	TimelineHasMore bool           `json:"timeline_has_more"`
}
