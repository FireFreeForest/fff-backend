package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
)

const (
	devicesResourceURI   = "firefreeforest://devices"
	activeEventsURI      = "firefreeforest://events/active"
	dashboardResourceURI = "firefreeforest://dashboard/summary"
	deviceLatestTemplate = "firefreeforest://devices/{device_id}/latest"
)

func registerResources(s *sdkmcp.Server, d Deps, role domain.UserRole) {
	addJSONResource(s, devicesResourceURI, "devices",
		"Daftar seluruh perangkat beserta status dan lokasinya.",
		func(ctx context.Context) (any, error) {
			rows, _, err := d.Repo.ListDevices(ctx, repository.DeviceFilter{Limit: 1000})
			if err != nil {
				return nil, err
			}
			return map[string]any{"data": rows}, nil
		})

	addJSONResource(s, activeEventsURI, "active_events",
		"Event yang masih aktif atau acknowledged, terbaru lebih dahulu.",
		func(ctx context.Context) (any, error) {
			rows, _, err := d.Repo.ListEvents(ctx, repository.EventFilter{
				Status: string(domain.EventActive),
				Limit:  100,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"data": rows}, nil
		})

	addJSONResource(s, dashboardResourceURI, "dashboard_summary",
		"Ringkasan operasional: perangkat, risk level, event aktif, dan event terbaru.",
		func(ctx context.Context) (any, error) {
			return buildDashboard(ctx, d)
		})

	s.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: deviceLatestTemplate,
		Name:        "device_latest",
		Description: "Status dan telemetri terbaru satu perangkat.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		uri := req.Params.URI
		deviceID, err := deviceIDFromLatestURI(uri)
		if err != nil {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		device, err := d.Repo.GetDeviceByDeviceID(ctx, deviceID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, sdkmcp.ResourceNotFoundError(uri)
			}
			return nil, err
		}
		latest := latestTelemetry(ctx, d.Redis, d.Repo, device)
		return jsonResource(uri, deviceStatusOutput{
			Device:        device,
			Status:        device.Status,
			LastSeenAt:    device.LastSeenAt,
			OnlineSince:   device.OnlineSince,
			UptimeSeconds: device.UptimeSeconds(nowUTC()),
			Environment:   latest.Environment,
			Fire:          latest.Fire,
			RiskLevel:     latest.RiskLevel,
			ServerTime:    nowUTC(),
		})
	})
}

func addJSONResource(s *sdkmcp.Server, uri, name, description string, load func(context.Context) (any, error)) {
	s.AddResource(&sdkmcp.Resource{
		URI:         uri,
		Name:        name,
		Description: description,
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		value, err := load(ctx)
		if err != nil {
			return nil, err
		}
		return jsonResource(req.Params.URI, value)
	})
}

func jsonResource(uri string, value any) (*sdkmcp.ReadResourceResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &sdkmcp.ReadResourceResult{
		Contents: []*sdkmcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(raw),
		}},
	}, nil
}

func deviceIDFromLatestURI(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	path := strings.Trim(parsed.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "latest" {
		return "", fmt.Errorf("invalid device latest URI")
	}
	deviceID := strings.TrimSpace(parts[0])
	if deviceID == "" {
		return "", fmt.Errorf("missing device_id")
	}
	return deviceID, nil
}
