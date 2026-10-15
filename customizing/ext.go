// Package customizing changes rows of customizing tables, as SM30 does: the
// key goes into a customizing request and the row is updated with it, under
// the table lock, with the new values checked against their check tables and
// fixed values.
//
// Only the tables the system lists may be changed, and only existing rows:
//
//	{"systems": {"dev": {"extensions": {"customizing": {"tables": ["/SCWM/TSTEP_FLOW"]}}}}}
//
// A change is an update (adt.OpUpdate): --read-only and --disallowed-ops U
// refuse it. It records into a transport request, so it needs
// --enable-transports, a request in allowed_transports, and no
// transport_read_only.
package customizing

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gorynychzmey/vsp-extensions/internal/tempreport"
	"github.com/gorynychzmey/vsp-extensions/internal/version"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
)

// Name is the extension's name, and the key of its settings in .vsp.json.
const Name = "customizing"

// Extension is the customizing extension.
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return Name }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	return []mcpext.Action{
		{Action: "read", Type: "TABROW", Class: mcpext.Read, Op: adt.OpFreeSQL, Handler: handleRead},
		{Action: "edit", Type: "TABROW", Class: mcpext.Mutate, Op: adt.OpUpdate, Handler: handleEdit},
	}
}

func (Extension) Help() string {
	return `Rows of customizing tables (delivery class C, G or E, client-specific), changed as SM30 does it:
the key is recorded in a customizing request (R3TR TABU) and the row is updated with it, under the
table lock, the new values checked against check tables and fixed values. Runs as a background job.
  SAP(action="read", target="TABROW /SCWM/TSTEP_FLOW", params={"key": {"APPLIC": "01", "PRES_PRF": "PARI",
      "LTRANS": "PAHRPA", "STEP": "PAPHIN", "FCODE": "ENTER"}})
  SAP(action="edit", target="TABROW /SCWM/TSTEP_FLOW", params={"key": {...},
      "set": {"FMODUL": "/PAR/RF_PACK_CREA_TO_PAI"}, "transport": "A4HK900001"})
"key" names every key field but the client; "set" the fields to change (character-like fields only);
"transport" a modifiable customizing request or a task of one. Only existing rows are changed, and the
row must not have changed since it was read. The answer shows the row before and after.
Only tables the system lists may be changed: "customizing": {"tables": ["/SCWM/TSTEP_FLOW"]} under
"extensions" in its .vsp.json entry. Needs --enable-transports and the request in allowed_transports;
transport_read_only refuses it. Create the request with
  SAP(action="system", params={"type": "create_transport", "request_type": "customizing", "description": "..."})`
}

// allowedTables is the system's opt-in: the tables whose rows may change.
func allowedTables(env mcpext.Env) []string {
	v, ok := env.Setting("tables")
	if !ok {
		return nil
	}
	var out []string
	switch list := v.(type) {
	case []any:
		for _, s := range list {
			if s, ok := s.(string); ok {
				out = append(out, strings.ToUpper(strings.TrimSpace(s)))
			}
		}
	case []string:
		for _, s := range list {
			out = append(out, strings.ToUpper(strings.TrimSpace(s)))
		}
	}
	return out
}

func checkTable(env mcpext.Env, table string) error {
	allowed := allowedTables(env)
	for _, t := range allowed {
		if t == table {
			return nil
		}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("changing table rows is off for this system; list the tables with "+
			`"extensions": {"%s": {"tables": ["%s"]}} in its .vsp.json entry`, Name, table)
	}
	return fmt.Errorf("table %s is not in the tables this system may change %v", table, allowed)
}

// stringMap reads a params object of field names to values.
func stringMap(params map[string]any, name string) (map[string]string, error) {
	v, ok := params[name]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`"%s" must be an object of field names to values`, name)
	}
	out := map[string]string{}
	for k, val := range m {
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf(`"%s.%s" must be a string, as the field is stored`, name, k)
		}
		out[k] = s
	}
	return out, nil
}

func tableOf(name string, params map[string]any) string {
	if strings.TrimSpace(name) == "" {
		name = mcpext.String(params, "table")
	}
	return strings.ToUpper(strings.TrimSpace(name))
}

func handleRead(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	table := tableOf(name, params)
	key, err := stringMap(params, "key")
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	return tempreport.Respond(ctx, env, "table rows", func(r tempreport.Runner) (tempreport.Outcome, error) {
		row, err := ReadRow(ctx, r, table, key)
		if err != nil {
			return tempreport.Outcome{}, err
		}
		return tempreport.Outcome{Result: map[string]any{"table": table, "row": row}}, nil
	})
}

func handleEdit(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	e := RowEdit{Table: tableOf(name, params), Transport: mcpext.String(params, "transport")}
	var err error
	if e.Key, err = stringMap(params, "key"); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	if e.Set, err = stringMap(params, "set"); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	if err := checkTable(env, e.Table); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	if strings.TrimSpace(e.Transport) == "" {
		return mcpext.Errorf(`a customizing request is required: params={"transport": "..."}`), nil
	}
	if env.System().ReadOnly {
		return mcpext.Errorf("blocked: read-only mode enabled"), nil
	}
	if err := env.ADT().Safety().CheckTransport(strings.ToUpper(strings.TrimSpace(e.Transport)), "EditTableRow", true); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	client := env.System().Client
	if len(client) != 3 {
		return mcpext.Errorf("the connected client is unknown; set it in .vsp.json"), nil
	}
	c := env.ADT()
	return tempreport.Respond(ctx, env, "table rows", func(r tempreport.Runner) (tempreport.Outcome, error) {
		res, err := EditRow(ctx, c, r, client, e)
		if res == nil {
			return tempreport.Outcome{}, err
		}
		return tempreport.Outcome{Result: res, Program: res.Program, Warnings: res.Warnings}, err
	})
}
