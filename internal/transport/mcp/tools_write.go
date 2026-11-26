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
	"firefreeforest/backend/internal/service"
)

type issuePumpInput struct {
	DeviceID       string `json:"device_id" jsonschema:"device_id publik perangkat"`
	State          bool   `json:"state" jsonschema:"true untuk menyalakan pompa, false untuk mematikan"`
	Duration       *int   `json:"duration,omitempty" jsonschema:"Durasi pompa menyala dalam detik (default AUTO_PUMP_DURATION, maksimum AUTO_PUMP_MAX_DURATION)"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"Kunci unik opsional agar retry tidak mengirim command ganda"`
}

func registerOperationalTools(s *sdkmcp.Server, d Deps, role domain.UserRole) {
	t := &tools{deps: d, role: role}

	sdkmcp.AddTool(s, writeTool("acknowledge_event",
		"Tandai event aktif sebagai acknowledged. Hanya berlaku untuk event berstatus active.",
		"Acknowledge Event", false), instrument(role, "acknowledge_event", t.acknowledgeEvent))

	sdkmcp.AddTool(s, writeTool("issue_pump_command",
		"Kirim perintah SET_PUMP ke perangkat. Gunakan idempotency_key agar retry aman.",
		"Kirim Perintah Pompa", true), instrument(role, "issue_pump_command", t.issuePumpCommand))

	sdkmcp.AddTool(s, writeTool("refresh_device_telemetry",
		"Kirim command GET_ALL_TELEMETRY agar perangkat segera mempublikasikan seluruh telemetri terbaru.",
		"Ambil Telemetri Sekarang", true), instrument(role, "refresh_device_telemetry", t.refreshDeviceTelemetry))

	if role == domain.RolePetugas {
		sdkmcp.AddTool(s, writeTool("resolve_event",
			"Tandai event sebagai resolved. Hanya petugas yang boleh menyelesaikan event.",
			"Resolve Event", false), instrument(role, "resolve_event", t.resolveEvent))
	}
}

func (t *tools) acknowledgeEvent(ctx context.Context, _ *sdkmcp.CallToolRequest, in eventInput) (*sdkmcp.CallToolResult, any, error) {
	principal, err := operationalPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
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
	if event.Status != domain.EventActive {
		return nil, nil, errors.New("event tidak berada pada status active")
	}
	updated, err := t.deps.Repo.AcknowledgeEvent(ctx, id, *principal.ActorID)
	if err != nil {
		return nil, nil, err
	}
	if !updated {
		return nil, nil, errors.New("event sudah diproses oleh pengguna lain")
	}
	auditMCP(ctx, t.deps.Repo, principal, "event.acknowledge", "event", id.String(), nil)
	publishEventUpdate(ctx, t.deps.Redis, t.deps.Repo, id, "event.updated")
	refreshed, err := t.deps.Repo.GetEvent(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(refreshed)
}

func (t *tools) resolveEvent(ctx context.Context, _ *sdkmcp.CallToolRequest, in eventInput) (*sdkmcp.CallToolResult, any, error) {
	principal, err := operationalPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
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
	if event.Status == domain.EventResolved {
		return nil, nil, errors.New("event sudah resolved")
	}
	updated, err := t.deps.Repo.ResolveEvent(ctx, id, *principal.ActorID)
	if err != nil {
		return nil, nil, err
	}
	if !updated {
		return nil, nil, errors.New("event tidak dapat di-resolve")
	}
	auditMCP(ctx, t.deps.Repo, principal, "event.resolve", "event", id.String(), nil)
	publishEventUpdate(ctx, t.deps.Redis, t.deps.Repo, id, "event.resolved")
	refreshed, err := t.deps.Repo.GetEvent(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(refreshed)
}

func (t *tools) issuePumpCommand(ctx context.Context, _ *sdkmcp.CallToolRequest, in issuePumpInput) (*sdkmcp.CallToolResult, any, error) {
	principal, err := operationalPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}

	duration := int(t.deps.Config.AutoPumpDuration.Seconds())
	if in.Duration != nil {
		duration = *in.Duration
	}
	if !in.State {
		duration = 0
	}
	maxDuration := int(t.deps.Config.AutoPumpMax.Seconds())
	if in.State && (duration < 1 || duration > maxDuration) {
		return nil, nil, fmt.Errorf("duration harus antara 1 dan %d detik", maxDuration)
	}

	idempotency := strings.TrimSpace(in.IdempotencyKey)
	redisKey := ""
	if idempotency != "" && t.deps.Redis != nil {
		redisKey = idempotencyKey(principal.KeyID, idempotency)
		acquired, err := t.deps.Redis.SetNX(ctx, redisKey, "pending", 24*time.Hour).Result()
		if err != nil {
			t.deps.Logger.Warn("mcp idempotency check failed", "error", err)
		} else if !acquired {
			existing, _ := t.deps.Redis.Get(ctx, redisKey).Result()
			if existing != "" && existing != "pending" {
				cmd, err := t.deps.Repo.GetCommandByCommandID(ctx, existing)
				if err == nil {
					return jsonResult(cmd)
				}
			}
			return nil, nil, errors.New("permintaan dengan idempotency_key yang sama sedang diproses")
		}
	}

	cmd, err := t.deps.Commands.Issue(ctx, device, service.IssueCommandRequest{
		Action:   domain.ActionSetPump,
		Payload:  map[string]any{"state": in.State, "duration": duration},
		Origin:   domain.OriginMCP,
		IssuedBy: principal.ActorID,
	})
	if err != nil {
		if redisKey != "" {
			t.deps.Redis.Del(ctx, redisKey)
		}
		if errors.Is(err, service.ErrValidation) {
			return nil, nil, err
		}
		return nil, nil, err
	}
	if redisKey != "" {
		t.deps.Redis.Set(ctx, redisKey, cmd.CommandID, 24*time.Hour)
	}
	auditMCP(ctx, t.deps.Repo, principal, "command.issue", "command", cmd.CommandID, map[string]any{
		"device_id": device.DeviceID,
		"action":    cmd.Action,
	})
	return jsonResult(cmd)
}

func (t *tools) refreshDeviceTelemetry(ctx context.Context, _ *sdkmcp.CallToolRequest, in deviceInput) (*sdkmcp.CallToolResult, any, error) {
	principal, err := operationalPrincipal(ctx)
	if err != nil {
		return nil, nil, err
	}
	device, err := lookupDevice(ctx, t.deps.Repo, in.DeviceID)
	if err != nil {
		return nil, nil, err
	}
	cmd, err := t.deps.Commands.Issue(ctx, device, service.IssueCommandRequest{
		Action:   domain.ActionGetAllTelemetry,
		Payload:  map[string]any{},
		Origin:   domain.OriginMCP,
		IssuedBy: principal.ActorID,
	})
	if err != nil {
		return nil, nil, err
	}
	auditMCP(ctx, t.deps.Repo, principal, "command.issue", "command", cmd.CommandID, map[string]any{
		"device_id": device.DeviceID,
		"action":    cmd.Action,
	})
	return jsonResult(cmd)
}

// operationalPrincipal requires a linked user because acknowledgements and
// resolutions store the acting user, and every action must be attributable.
func operationalPrincipal(ctx context.Context) (*service.MCPPrincipal, error) {
	principal := PrincipalFromContext(ctx)
	if principal == nil {
		return nil, errors.New("konteks autentikasi MCP tidak tersedia")
	}
	if principal.ActorID == nil {
		return nil, errors.New("API key ini belum terhubung ke user (actor_id); tindakan operasional tidak diizinkan")
	}
	return principal, nil
}
