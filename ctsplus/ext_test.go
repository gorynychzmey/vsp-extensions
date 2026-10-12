package ctsplus

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
	"github.com/oisee/vibing-steampunk/pkg/mcpext/mcpexttest"
)

func envWith(safety adt.SafetyConfig, settings map[string]any) *mcpexttest.Env {
	c := adt.NewClient("http://127.0.0.1:1", "", "", adt.WithSafety(safety))
	return &mcpexttest.Env{Client: c, Settings: settings, SystemInfo: mcpext.System{Client: "100"}}
}

func TestExtensionIsWellBehaved(t *testing.T) {
	mcpexttest.Check(t, New())
}

// The gates before any RFC: transports enabled, the request whitelist, and
// transport_read_only for a change to the request but not for an import.
func TestGates(t *testing.T) {
	on := adt.SafetyConfig{EnableTransports: true, AllowedTransports: []string{"D11K*", "D22K*"}}
	tro := on
	tro.TransportReadOnly = true
	imp := map[string]any{"import_systems": []any{"Q22"}}
	for name, tc := range map[string]struct {
		safety   adt.SafetyConfig
		settings map[string]any
		handler  mcpext.Handler
		target   string
		params   map[string]any
		want     string
	}{
		"edit, transports off":         {adt.SafetyConfig{}, nil, handleEdit, "D22K900240", map[string]any{"project": "D11_P00005"}, "not enabled"},
		"edit, outside whitelist":      {on, nil, handleEdit, "A4HK900001", map[string]any{"project": "D11_P00005"}, "allowed"},
		"edit, transport read-only":    {tro, nil, handleEdit, "D22K900240", map[string]any{"project": "D11_P00005"}, "transport read-only"},
		"edit, no project":             {on, nil, handleEdit, "D22K900240", nil, "project"},
		"release, transport read-only": {tro, nil, handleRelease, "D22K900240", nil, "transport read-only"},
		"release, not a request":       {on, nil, handleRelease, "D22K9002' OR", nil, "not a request number"},
		"import, no opt-in":            {on, nil, handleImport, "D22K900240", map[string]any{"system": "Q22"}, "off for this system"},
		"import, other target":         {on, imp, handleImport, "D22K900240", map[string]any{"system": "P22"}, "not in import_systems"},
		"import, no target":            {on, imp, handleImport, "D22K900240", nil, "target system"},
		"import, outside whitelist":    {on, imp, handleImport, "A4HK900001", map[string]any{"system": "Q22"}, "allowed"},
		// Passes every gate and fails only at the missing RFC connection.
		"import, transport read-only": {tro, imp, handleImport, "D22K900240", map[string]any{"system": "q22"}, "classic RFC"},
	} {
		res, err := tc.handler(context.Background(), envWith(tc.safety, tc.settings), tc.target, tc.params)
		if err != nil || res == nil || !res.IsError {
			t.Errorf("%s: %+v %v", name, res, err)
			continue
		}
		text, _ := res.Content[0].(mcp.TextContent)
		if !strings.Contains(text.Text, tc.want) {
			t.Errorf("%s: %q, want one naming %q", name, text.Text, tc.want)
		}
	}
}
