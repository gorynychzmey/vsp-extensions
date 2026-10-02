// Package trimport imports released transport requests into the connected
// system, as STMS_IMPORT does there.
//
// The import changes the system, so every gate applies: --read-only refuses
// it, it is a transport operation (adt.OpTransport) and so needs
// --enable-transports and is refused by --disallowed-ops X, and
// allowed_transports limits which requests it takes. On top of that it is off
// unless the system's own setting allows it:
//
//	{"systems": {"qas": {"extensions": {"transport-import": {"allow": true}}}}}
//
// transport_read_only does not refuse it. That switch guards the requests
// themselves -- creating, changing, releasing them -- which an import does
// not touch. A quality system that should take imports and nothing else runs
// with transport_read_only and --disallowed-ops CDUAWLT instead of
// --read-only.
package trimport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"github.com/gorynychzmey/vsp-extensions/internal/version"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
)

// Name is the extension's name, and the key of its settings in .vsp.json.
const Name = "transport-import"

// Extension is the transport import extension.
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return Name }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	return []mcpext.Action{
		{Action: "import", Type: "TRANSPORT", Class: mcpext.Mutate, Op: adt.OpTransport, Handler: handleImport},
	}
}

func (Extension) Help() string {
	return `Import released requests into this server's own system, as STMS_IMPORT does there (TMS over classic
RFC, CTS_API_IMPORT_CHANGE_REQUEST). Off unless the system allows it ("transport-import": {"allow": true}
under "extensions" in its .vsp.json entry); needs --enable-transports, and allowed_transports limits
the requests:
  SAP(action="import", target="TRANSPORT D11K900001")
  SAP(action="import", target="TRANSPORT D11K900001,D11K900002", params={"client": "200"})
  SAP(action="import", target="TRANSPORT D11K900001", params={"async": true})   returns a task at once
"timeout" (seconds, default 3600) bounds the import. An import into production runs for minutes; with
"async" follow it with SAP(action="debug", target="GET_ASYNC_RESULT", params={"task_id": "...", "wait_seconds": 600})
or read TPALOG with SAP(action="system", params={"type": "import_status", "transport": "..."}).`
}

// Commands adds `vsp transport import`.
func (Extension) Commands(env mcpext.EnvFunc) []mcpext.Command {
	cmd := &cobra.Command{
		Use:   "import <REQUEST> [REQUEST ...]",
		Short: "Import released requests into this system, as STMS_IMPORT does there (classic RFC)",
		Long: `Import released requests into the system named by -s, in its client or
--client, through TMS -- CTS_API_IMPORT_CHANGE_REQUEST over classic RFC,
called in that system itself, which is where STMS_IMPORT runs too. From the
domain controller TMS would need the target's TMSSUP logon, which a remote
call cannot give, so the target is always the connected system.

The import changes the system, so it is off unless the system allows it
("transport-import": {"allow": true} under "extensions" in .vsp.json). It
needs enable_transports; read_only and --disallowed-ops X refuse it, and
allowed_transports limits which requests it takes.

  vsp -s qas transport import D11K900001
  vsp -s qas transport import D11K900001 D11K900002 --client 200 --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := env(cmd)
			if err != nil {
				return err
			}
			reqs, err := checkAllowed(e, args)
			if err != nil {
				return err
			}
			client, _ := cmd.Flags().GetString("client")
			if client == "" {
				client = e.System().Client
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			res, ierr := run(cmd.Context(), e, reqs, client, timeout)
			switch {
			case res == nil:
			case asJSON:
				out, _ := json.MarshalIndent(res, "", "  ")
				fmt.Println(string(out))
			default:
				for _, r := range res.Requests {
					fmt.Fprintf(os.Stderr, "%s -> %s client %s: retcode %s, %d tp step(s), worst rc %s\n",
						r.Request, res.System, res.Client, r.RetCode, len(r.Steps), r.MaxRC)
				}
				fmt.Fprintln(os.Stderr, res.Message)
			}
			return ierr
		},
	}
	cmd.Flags().String("client", "", "Target client (default: the system's client)")
	cmd.Flags().Bool("json", false, "Emit JSON")
	cmd.Flags().Duration("timeout", ImportTimeout, "How long to wait for the import (tp runs while the call waits)")
	return []mcpext.Command{{Parent: "transport", Command: cmd}}
}

// checkAllowed applies the gates to an import of requests and returns them
// normalized. The MCP server has already refused it under --read-only and
// checked the operation; a command line has not, so both are checked here as
// well.
func checkAllowed(env mcpext.Env, requests []string) ([]string, error) {
	if v, ok := env.Setting("allow"); !ok || v != true {
		return nil, fmt.Errorf("importing requests is off for this system; enable it with "+
			`"extensions": {"%s": {"allow": true}} in its .vsp.json entry`, Name)
	}
	if env.System().ReadOnly {
		return nil, fmt.Errorf("transport import is blocked: read-only mode enabled")
	}
	safety := env.ADT().Safety()
	if err := safety.CheckOperation(adt.OpTransport, "TransportImport"); err != nil {
		return nil, err
	}
	reqs, err := normalizedRequests(requests)
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		if !safety.IsTransportAllowed(r) {
			return nil, fmt.Errorf("transport import of %s is blocked: not in allowed_transports %v", r, safety.AllowedTransports)
		}
	}
	return reqs, nil
}

// run imports on a connection of its own: the shared one gives up after the
// library's 30 s, and a production import runs far longer than that.
func run(ctx context.Context, env mcpext.Env, reqs []string, client string, timeout time.Duration) (*ImportResult, error) {
	c, err := env.RFCDedicated(ctx, timeout)
	if err != nil {
		return nil, fmt.Errorf("importing needs classic RFC to this system: %w", err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	return ImportRequests(ctx, c, reqs, client)
}

// destinationOverrides would point a call at another system; the import
// always goes to the server's own.
var destinationOverrides = []string{"host", "sysnr", "port", "user"}

func handleImport(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	for _, key := range destinationOverrides {
		if _, set := params[key]; set {
			return mcpext.Errorf("import does not take %q: the import always goes to this server's own system "+
				"(set rfc_host/rfc_sysnr/rfc_port/rfc_user in its .vsp.json entry instead)", key), nil
		}
	}
	requests := splitList(name)
	if len(requests) == 0 {
		requests = mcpext.Strings(params, "transport")
	}
	if len(requests) == 0 {
		requests = mcpext.Strings(params, "transports")
	}
	reqs, err := checkAllowed(env, requests)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	client := mcpext.String(params, "client")
	if client == "" {
		client = env.System().Client
	}
	timeout := ImportTimeout
	if secs := mcpext.Int(params, "timeout", 0); secs > 0 {
		timeout = time.Duration(secs) * time.Second
	}

	if async, _ := mcpext.Bool(params, "async"); async {
		started := time.Now()
		id, err := env.StartAsync("import", func(ctx context.Context) (any, error) {
			// Bounded by its own timeout, not by the call that started it.
			ctx, cancel := context.WithTimeout(ctx, timeout+time.Minute)
			defer cancel()
			return run(ctx, env, reqs, client, timeout)
		})
		if err != nil {
			return mcpext.Errorf("%v", err), nil
		}
		since := started.UTC().Format("20060102150405")
		return mcpext.JSON(map[string]any{
			"task_id":  id,
			"status":   "started",
			"requests": reqs,
			"client":   client,
			"since":    since,
			"follow":   fmt.Sprintf(`SAP(action="debug", target="GET_ASYNC_RESULT", params={"task_id": %q, "wait_seconds": 600})`, id),
			"log": fmt.Sprintf(`SAP(action="system", params={"type": "import_status", "transport": %q, "since": %q})`,
				strings.Join(reqs, ","), since),
		}), nil
	}
	res, err := run(ctx, env, reqs, client, timeout)
	if err != nil {
		// The partial result stays, but the call is an error: a non-000 TMS
		// code must not read as a successful tool call.
		out := mcpext.JSON(map[string]any{"error": err.Error(), "result": res})
		out.IsError = true
		return out, nil
	}
	return mcpext.JSON(res), nil
}

// splitList reads one request or a comma-separated list.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
