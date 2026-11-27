package api

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return spec
}

func TestOpenAPISpecIsValidYAML(t *testing.T) {
	spec := loadSpec(t)
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi = %v", spec["openapi"])
	}
	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatalf("info = %T", spec["info"])
	}
	if title, _ := info["title"].(string); title == "" {
		t.Fatalf("info.title = %v", info["title"])
	}
	if _, ok := spec["paths"].(map[string]any); !ok {
		t.Fatalf("paths = %T", spec["paths"])
	}
	if _, ok := spec["components"].(map[string]any); !ok {
		t.Fatalf("components = %T", spec["components"])
	}
}

func TestOpenAPIHasNoMCPKeyManagement(t *testing.T) {
	spec := loadSpec(t)
	paths, _ := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/admin/mcp-keys",
		"/admin/mcp-keys/{keyID}",
		"/admin/mcp-keys/{keyID}/rotate",
	} {
		if _, ok := paths[path]; ok {
			t.Fatalf("path %q must be removed", path)
		}
	}

	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for _, name := range []string{"MCPAPIKey", "MCPAPIKeyCreated", "MCPAPIKeyRotated"} {
		if _, ok := schemas[name]; ok {
			t.Fatalf("schema %q must be removed", name)
		}
	}
	parameters, _ := components["parameters"].(map[string]any)
	if _, ok := parameters["MCPKeyID"]; ok {
		t.Fatal("parameter MCPKeyID must be removed")
	}
}

func TestOpenAPIReferencesResolve(t *testing.T) {
	spec := loadSpec(t)
	checkRefs(t, spec, spec, "#")
}

func checkRefs(t *testing.T, node, root any, location string) {
	t.Helper()
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
			if !refExists(root, ref) {
				t.Fatalf("unresolved reference %q at %s", ref, location)
			}
		}
		for key, child := range value {
			checkRefs(t, child, root, location+"/"+key)
		}
	case []any:
		for index, child := range value {
			checkRefs(t, child, root, fmt.Sprintf("%s/%d", location, index))
		}
	}
}

func refExists(root any, ref string) bool {
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[part]
		if !ok {
			return false
		}
	}
	return true
}
