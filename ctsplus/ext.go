// Package ctsplus handles non-ABAP requests (CTS+) on the domain controller:
// it reads them, assigns their CTS project, releases them and imports them
// into the other systems of the transport domain -- the steps the CTS
// Organizer Web UI and STMS take for a PI or other non-ABAP system.
//
// The server runs against the domain controller (for PI system D22 with
// requests D22K..., the ABAP system whose TMS holds DOMAIN_D11). Every
// change is a transport operation (adt.OpTransport): it needs
// --enable-transports, --read-only and --disallowed-ops X refuse it, and
// allowed_transports limits the requests. Assigning a project and releasing
// change the request, so transport_read_only refuses them as well; importing
// does not touch the request and is off unless the system names the target
// systems it may import into:
//
//	{"systems": {"dev": {"extensions": {"cts-plus": {"import_systems": ["Q22"]}}}}}
package ctsplus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/open-rfc-go/rfc"

	"github.com/gorynychzmey/vsp-extensions/internal/version"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
)

// Name is the extension's name, and the key of its settings in .vsp.json.
const Name = "cts-plus"

// ImportTimeout bounds an import call: tp runs while the call waits.
const ImportTimeout = 30 * time.Minute

// Extension is the CTS+ extension.
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return Name }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	return []mcpext.Action{
		{Action: "read", Type: "CTSREQUEST", Class: mcpext.Read, Op: adt.OpRead, Handler: handleRead},
		{Action: "read", Type: "CTSQUEUE", Class: mcpext.Read, Op: adt.OpRead, Handler: handleQueue},
		{Action: "edit", Type: "CTSREQUEST", Class: mcpext.Mutate, Op: adt.OpTransport, Handler: handleEdit},
		{Action: "release", Type: "CTSREQUEST", Class: mcpext.Mutate, Op: adt.OpTransport, Handler: handleRelease},
		{Action: "import", Type: "CTSREQUEST", Class: mcpext.Mutate, Op: adt.OpTransport, Handler: handleImport},
	}
}

func (Extension) Help() string {
	return `Non-ABAP requests (CTS+, e.g. PI requests D22K...) on the domain controller: read, assign the CTS
project, release, import into a system of the transport domain -- over classic RFC, as the CTS
Organizer Web UI and STMS do it. The classic TR_* function modules refuse these requests there.
  SAP(action="read", target="CTSREQUEST D22K900240")                header, state, CTS project, attributes
  SAP(action="edit", target="CTSREQUEST D22K900240", params={"project": "D11_P00005"})
  SAP(action="release", target="CTSREQUEST D22K900240", params={"project": "D11_P00005"})
      "project" is assigned first when the request has none (CTS refuses the release without one)
  SAP(action="read", target="CTSQUEUE Q22", params={"request": "D22K900240"})   import queue; "status", "request" filter
  SAP(action="import", target="CTSREQUEST D22K900240", params={"system": "Q22"})
      "async": true returns a task (GET_ASYNC_RESULT); "domain" if the system is not in the controller's own domain
edit, release and import need --enable-transports and a request in allowed_transports; edit and release
are refused by transport_read_only. import is off unless the target is listed:
"cts-plus": {"import_systems": ["Q22"]} under "extensions" in the system's .vsp.json entry.
A typical chain after a PI export: release (with project) -> import into Q22 -> system import_status.`
}

// gate applies what the core does not know about this extension: the
// request whitelist and, for a change to the request, transport_read_only.
// The server has already refused --read-only and checked OpTransport.
func gate(env mcpext.Env, req string, changesRequest bool) error {
	if env.System().ReadOnly {
		return fmt.Errorf("blocked: read-only mode enabled")
	}
	safety := env.ADT().Safety()
	return safety.CheckTransport(req, "CTSPlus", changesRequest)
}

// importSystems is the system's opt-in: the systems it may import into.
func importSystems(env mcpext.Env) []string {
	v, ok := env.Setting("import_systems")
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

func checkImportTarget(env mcpext.Env, system string) error {
	allowed := importSystems(env)
	if contains(allowed, system) {
		return nil
	}
	if len(allowed) == 0 {
		return fmt.Errorf("importing is off for this system; list the targets with "+
			`"extensions": {"%s": {"import_systems": ["%s"]}} in its .vsp.json entry`, Name, system)
	}
	return fmt.Errorf("import into %s is blocked: not in import_systems %v", system, allowed)
}

// ownSystem names the connected system, so a request of its own -- an ABAP
// request, which the built-in transport actions handle with their checks --
// is told apart from a non-ABAP one.
func ownSystem(ctx context.Context, call callFn) (string, error) {
	info, err := call(ctx, "RFC_SYSTEM_INFO", rfc.Params{})
	if err != nil {
		return "", fmt.Errorf("reading the system ID: %w", err)
	}
	if m, ok := info.Get("RFCSI_EXPORT").(map[string]any); ok {
		if sid := str(m["RFCSYSID"]); sid != "" {
			return sid, nil
		}
	}
	return "", fmt.Errorf("the connected system did not name itself (RFC_SYSTEM_INFO)")
}

func refuseOwnRequest(ctx context.Context, call callFn, req string) error {
	sid, err := ownSystem(ctx, call)
	if err != nil {
		return err
	}
	if strings.HasPrefix(req, sid+"K") {
		return fmt.Errorf("%s is a request of %s itself; the built-in transport actions handle it", req, sid)
	}
	return nil
}

// withRFC runs fn on the server's shared RFC client.
func withRFC(ctx context.Context, env mcpext.Env, fn func(call callFn) (any, error)) (*mcp.CallToolResult, error) {
	c, release, err := env.RFC(ctx)
	if err != nil {
		return mcpext.Errorf("CTS+ needs classic RFC to the domain controller: %v", err), nil
	}
	defer release()
	res, err := fn(clientCall(c))
	if err != nil {
		if rfcDead(err) {
			env.DropRFC(ctx)
		}
		if res == nil {
			return mcpext.Errorf("%v", err), nil
		}
		out := mcpext.JSON(map[string]any{"error": err.Error(), "result": res})
		out.IsError = true
		return out, nil
	}
	return mcpext.JSON(res), nil
}

func rfcDead(err error) bool {
	return errors.Is(err, rfc.ErrTransport) || errors.Is(err, rfc.ErrClosed)
}

func handleRead(ctx context.Context, env mcpext.Env, name string, _ map[string]any) (*mcp.CallToolResult, error) {
	req, err := normalizedRequest(name)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	return withRFC(ctx, env, func(call callFn) (any, error) {
		r, err := details(ctx, call, req)
		if r == nil {
			return nil, err
		}
		return r, err
	})
}

func handleQueue(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	system := strings.ToUpper(strings.TrimSpace(name))
	if !systemPattern.MatchString(system) {
		return mcpext.Errorf("%q is not a system ID", name), nil
	}
	domain := strings.ToUpper(mcpext.String(params, "domain"))
	if domain != "" && !domainPattern.MatchString(domain) {
		return mcpext.Errorf("%q is not a transport domain", domain), nil
	}
	status := strings.ToUpper(mcpext.String(params, "status"))
	wanted := map[string]bool{}
	for _, r := range splitList(strings.Join(append(mcpext.Strings(params, "request"), mcpext.Strings(params, "requests")...), ",")) {
		wanted[strings.ToUpper(r)] = true
	}
	limit := mcpext.Int(params, "limit", 50)
	return withRFC(ctx, env, func(call callFn) (any, error) {
		items, err := queue(ctx, call, system, domain)
		if err != nil {
			return nil, err
		}
		counts := map[string]int{}
		var shown []QueueItem
		for _, it := range items {
			counts[it.Status]++
			if status != "" && it.Status != status {
				continue
			}
			if len(wanted) > 0 && !wanted[it.ID] {
				continue
			}
			shown = append(shown, it)
		}
		// The queue keeps imported requests too; the newest are last.
		total := len(shown)
		if len(wanted) == 0 && limit > 0 && len(shown) > limit {
			shown = shown[len(shown)-limit:]
		}
		res := map[string]any{"system": system, "total": len(items), "byStatus": counts, "matching": total, "items": shown}
		if len(wanted) > 0 && len(shown) < len(wanted) {
			res["note"] = "a request released moments ago may not be listed yet; import takes it by number all the same"
		}
		return res, nil
	})
}

func handleEdit(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	req, err := normalizedRequest(name)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	project := mcpext.String(params, "project")
	if project == "" {
		return mcpext.Errorf(`edit CTSREQUEST changes the CTS project only: params={"project": "..."}`), nil
	}
	if err := gate(env, req, true); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	return withRFC(ctx, env, func(call callFn) (any, error) {
		if err := refuseOwnRequest(ctx, call, req); err != nil {
			return nil, err
		}
		return nilIfEmpty(setProjectChecked(ctx, call, req, project))
	})
}

func handleRelease(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	req, err := normalizedRequest(name)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	if err := gate(env, req, true); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	project := mcpext.String(params, "project")
	return withRFC(ctx, env, func(call callFn) (any, error) {
		if err := refuseOwnRequest(ctx, call, req); err != nil {
			return nil, err
		}
		return nilIfEmpty(release(ctx, call, req, project))
	})
}

// nilIfEmpty keeps a typed nil *Request from reading as a result.
func nilIfEmpty(r *Request, err error) (any, error) {
	if r == nil {
		return nil, err
	}
	return r, err
}

func handleImport(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	var reqs []string
	for _, r := range splitList(name) {
		n, err := normalizedRequest(r)
		if err != nil {
			return mcpext.Errorf("%v", err), nil
		}
		reqs = append(reqs, n)
	}
	if len(reqs) == 0 {
		return mcpext.Errorf("at least one request to import is required"), nil
	}
	system := strings.ToUpper(mcpext.String(params, "system"))
	if !systemPattern.MatchString(system) {
		return mcpext.Errorf(`import CTSREQUEST needs the target system: params={"system": "Q22"}`), nil
	}
	domain := strings.ToUpper(mcpext.String(params, "domain"))
	if domain != "" && !domainPattern.MatchString(domain) {
		return mcpext.Errorf("%q is not a transport domain", domain), nil
	}
	for _, r := range reqs {
		if err := gate(env, r, false); err != nil {
			return mcpext.Errorf("%v", err), nil
		}
	}
	if err := checkImportTarget(env, system); err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	timeout := ImportTimeout
	if secs := mcpext.Int(params, "timeout", 0); secs > 0 {
		timeout = time.Duration(secs) * time.Second
	}
	run := func(ctx context.Context) (*ImportResult, error) {
		c, err := env.RFCDedicated(ctx, timeout)
		if err != nil {
			return nil, fmt.Errorf("CTS+ needs classic RFC to the domain controller: %w", err)
		}
		defer func() { _ = c.Close(context.Background()) }()
		return importItems(ctx, clientCall(c), clientLog(c), reqs, system, domain)
	}

	if async, _ := mcpext.Bool(params, "async"); async {
		id, err := env.StartAsync("cts-import", func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout+time.Minute)
			defer cancel()
			return run(ctx)
		})
		if err != nil {
			return mcpext.Errorf("%v", err), nil
		}
		return mcpext.JSON(map[string]any{
			"task_id":  id,
			"status":   "started",
			"requests": reqs,
			"system":   system,
			"follow":   fmt.Sprintf(`SAP(action="debug", target="GET_ASYNC_RESULT", params={"task_id": %q, "wait_seconds": 600})`, id),
		}), nil
	}
	res, err := run(ctx)
	if err != nil {
		if res == nil {
			return mcpext.Errorf("%v", err), nil
		}
		out := mcpext.JSON(map[string]any{"error": err.Error(), "result": res})
		out.IsError = true
		return out, nil
	}
	return mcpext.JSON(res), nil
}

// splitList reads one value or a comma-separated list.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
