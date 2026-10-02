package trimport

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
	"github.com/oisee/vibing-steampunk/pkg/mcpext/mcpexttest"
)

func envWith(safety adt.SafetyConfig, settings map[string]any, readOnly bool) *mcpexttest.Env {
	c := adt.NewClient("http://127.0.0.1:1", "", "", adt.WithSafety(safety))
	return &mcpexttest.Env{Client: c, Settings: settings, SystemInfo: mcpext.System{Client: "100", ReadOnly: readOnly}}
}

// The gates, each on its own: the system's opt-in, read-only, transports
// enabled, --disallowed-ops X, the transport whitelist. Transport read-only
// does not refuse an import.
func TestCheckAllowed(t *testing.T) {
	allow := map[string]any{"allow": true}
	on := adt.SafetyConfig{EnableTransports: true}
	for name, tc := range map[string]struct {
		safety   adt.SafetyConfig
		settings map[string]any
		readOnly bool
		want     string
	}{
		"allowed":                   {on, allow, false, ""},
		"transport read-only":       {adt.SafetyConfig{EnableTransports: true, TransportReadOnly: true}, allow, false, ""},
		"no opt-in":                 {on, nil, false, "off for this system"},
		"opt-in not a boolean":      {on, map[string]any{"allow": "yes"}, false, "off for this system"},
		"read-only":                 {on, allow, true, "read-only"},
		"transports off":            {adt.SafetyConfig{}, allow, false, "TransportImport"},
		"transport ops disallowed":  {adt.SafetyConfig{EnableTransports: true, DisallowedOps: "X"}, allow, false, "TransportImport"},
		"outside allowed":           {adt.SafetyConfig{EnableTransports: true, AllowedTransports: []string{"A4HK*"}}, allow, false, "D11K900001"},
		"other writes disallowed":   {adt.SafetyConfig{EnableTransports: true, DisallowedOps: "CDUAWLT"}, allow, false, ""},
		"not a request (injection)": {on, allow, false, ""},
	} {
		reqs := []string{"d11k900001"}
		if name == "not a request (injection)" {
			reqs, tc.want = []string{"X' OR '1'='1"}, "not a request number"
		}
		got, err := checkAllowed(envWith(tc.safety, tc.settings, tc.readOnly), reqs)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: refused: %v", name, err)
		case tc.want == "" && (len(got) != 1 || got[0] != "D11K900001"):
			t.Errorf("%s: requests = %v", name, got)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
}

// The import goes to the server's own system only: a per-call destination is
// refused before anything else.
func TestHandleImportRefusesADestinationOverride(t *testing.T) {
	env := envWith(adt.SafetyConfig{EnableTransports: true}, map[string]any{"allow": true}, false)
	res, err := handleImport(context.Background(), env, "D11K900001", map[string]any{"host": "10.0.0.1"})
	text, _ := res.Content[0].(mcp.TextContent)
	if err != nil || !res.IsError || !strings.Contains(text.Text, "own system") {
		t.Errorf("host override: %+v %v", res, err)
	}
}
