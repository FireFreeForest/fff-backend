package mcp

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectTestServer(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	server := NewServer(Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, session *sdkmcp.ClientSession) map[string]bool {
	t.Helper()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestToolCatalogIsReadOnly(t *testing.T) {
	readTools := []string{
		"list_devices", "get_device", "get_device_status", "get_latest_telemetry",
		"list_environment_telemetry", "aggregate_environment_telemetry",
		"list_fire_telemetry", "aggregate_fire_telemetry",
		"list_device_telemetry",
		"list_events", "get_event", "list_event_timeline",
		"list_commands", "get_command", "get_dashboard_summary", "reverse_geocode",
	}
	forbidden := []string{
		"acknowledge_event", "resolve_event", "issue_pump_command", "refresh_device_telemetry",
	}

	session := connectTestServer(t)
	names := toolNames(t, session)
	for _, name := range readTools {
		if !names[name] {
			t.Fatalf("tool %q missing", name)
		}
	}
	for _, name := range forbidden {
		if names[name] {
			t.Fatalf("write tool %q must not be exposed", name)
		}
	}
}

func TestToolsAreAnnotatedReadOnly(t *testing.T) {
	session := connectTestServer(t)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range result.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %q is not marked read-only", tool.Name)
		}
	}
}

func TestResourcesAndPrompts(t *testing.T) {
	session := connectTestServer(t)

	resources, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	uris := map[string]bool{}
	for _, resource := range resources.Resources {
		uris[resource.URI] = true
	}
	for _, uri := range []string{devicesResourceURI, activeEventsURI, dashboardResourceURI} {
		if !uris[uri] {
			t.Fatalf("resource %q missing", uri)
		}
	}

	templates, err := session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(templates.ResourceTemplates) != 1 || templates.ResourceTemplates[0].URITemplate != deviceLatestTemplate {
		t.Fatalf("templates = %+v", templates.ResourceTemplates)
	}

	prompts, err := session.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) != 3 {
		t.Fatalf("prompts = %d, want 3", len(prompts.Prompts))
	}
	for _, prompt := range prompts.Prompts {
		if prompt.Name == "pump_command_checklist" {
			t.Fatal("pump_command_checklist must not be exposed")
		}
	}

	prompt, err := session.GetPrompt(context.Background(), &sdkmcp.GetPromptParams{Name: "triage_events"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if prompt.Description == "" || len(prompt.Messages) != 1 {
		t.Fatalf("prompt = %+v", prompt)
	}

	if _, err := session.GetPrompt(context.Background(), &sdkmcp.GetPromptParams{Name: "device_health"}); err == nil {
		t.Fatal("device_health without device_id should fail")
	}
}
