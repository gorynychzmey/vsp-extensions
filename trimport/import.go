package trimport

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// Importing a released request is what STMS_IMPORT does in the target system.
// CTS_API_IMPORT_CHANGE_REQUEST is SAP's remote-enabled CTS API for it: it
// hands the request to TMS, which runs tp. Called in the target system itself,
// TMS works locally under the caller's own logon. Called from the domain
// controller for another system, TMS needs that system's TMSSUP destination,
// which asks for a logon a remote call cannot give -- so ImportRequests always
// imports into the system it is connected to.

// exports is what a call returns; rfc.Result is one.
type exports interface {
	Get(name string) any
	Table(name string) []map[string]any
}

type callFn func(ctx context.Context, fm string, in rfc.Params) (exports, error)

// logFn reads the TPALOG steps of requests, by request, oldest first.
type logFn func(ctx context.Context, requests []string) (map[string][]saprfc.ImportStep, error)

// ImportTimeout is how long an import call may run. tp works while
// CTS_API_IMPORT_CHANGE_REQUEST waits, and a production import can take many
// minutes; a client that gives up after the library's default of 30 s
// reports a failure for an import that goes on to finish.
const ImportTimeout = 60 * time.Minute

// ImportedRequest is one request of an import.
type ImportedRequest struct {
	Request string `json:"request"`
	// RetCode is TMS's code for the request (see retCodeText): 000 imported
	// (tp rc 0-7), 008 TMS could not start, 009 tp rc 8, 010 tp rc above 8,
	// 011 no authorization, 012 another TMS error, 013 log unreadable.
	RetCode string `json:"retcode"`
	// Steps are the TPALOG rows this import added, and MaxRC the worst of them.
	Steps []saprfc.ImportStep `json:"steps,omitempty"`
	MaxRC string              `json:"maxRc,omitempty"`
}

// ImportResult is what an import did.
type ImportResult struct {
	System   string            `json:"system"`
	Client   string            `json:"client"`
	RetCode  string            `json:"retcode"`
	Message  string            `json:"message"`
	Imported bool              `json:"imported"`
	Requests []ImportedRequest `json:"requests"`
}

var (
	requestPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)
	clientPattern  = regexp.MustCompile(`^[0-9]{3}$`)
)

// ImportRequests imports released requests into the connected system, in
// client, through TMS -- as STMS_IMPORT does there -- and reports the tp steps
// the import added.
func ImportRequests(ctx context.Context, c *rfc.Client, requests []string, client string) (*ImportResult, error) {
	call := func(ctx context.Context, fm string, in rfc.Params) (exports, error) { return c.Call(ctx, fm, in) }
	readLog := func(ctx context.Context, reqs []string) (map[string][]saprfc.ImportStep, error) {
		logs, err := saprfc.ReadImportLog(ctx, c, reqs, "")
		if err != nil {
			return nil, err
		}
		out := map[string][]saprfc.ImportStep{}
		for _, l := range logs {
			out[l.Request] = l.Steps
		}
		return out, nil
	}
	return importRequests(ctx, call, readLog, requests, client)
}

// normalizedRequests upper-cases the requests and refuses anything that is
// not a request number.
func normalizedRequests(requests []string) ([]string, error) {
	var reqs []string
	for _, r := range requests {
		r = strings.ToUpper(strings.TrimSpace(r))
		if !requestPattern.MatchString(r) {
			return nil, fmt.Errorf("%q is not a request number", r)
		}
		reqs = append(reqs, r)
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("at least one request to import is required")
	}
	return reqs, nil
}

func importRequests(ctx context.Context, call callFn, readLog logFn, requests []string, client string) (*ImportResult, error) {
	reqs, err := normalizedRequests(requests)
	if err != nil {
		return nil, err
	}
	if !clientPattern.MatchString(client) {
		return nil, fmt.Errorf("a three-digit target client is required, got %q", client)
	}

	info, err := call(ctx, "RFC_SYSTEM_INFO", rfc.Params{})
	if err != nil {
		return nil, fmt.Errorf("reading the system ID: %w", err)
	}
	sid := ""
	if m, ok := info.Get("RFCSI_EXPORT").(map[string]any); ok {
		sid = strings.TrimSpace(fmt.Sprint(m["RFCSYSID"]))
	}
	if sid == "" {
		return nil, fmt.Errorf("the connected system did not name itself (RFC_SYSTEM_INFO)")
	}

	before, err := readLog(ctx, reqs)
	if err != nil {
		return nil, fmt.Errorf("reading TPALOG before the import: %w", err)
	}

	in := rfc.Params{"SYSTEM": sid, "CLIENT": client}
	var table []map[string]any
	for _, r := range reqs {
		table = append(table, map[string]any{"REQUEST": r})
	}
	in["REQUESTS"] = table
	out, err := call(ctx, "CTS_API_IMPORT_CHANGE_REQUEST", in)
	if err != nil {
		return nil, fmt.Errorf("CTS_API_IMPORT_CHANGE_REQUEST: %w", err)
	}

	res := &ImportResult{
		System:  sid,
		Client:  client,
		RetCode: strings.TrimSpace(fmt.Sprint(out.Get("RETCODE"))),
		Message: strings.TrimSpace(fmt.Sprint(out.Get("MESSAGE"))),
	}
	perRequest := map[string]string{}
	for _, row := range out.Table("REQUESTS") {
		perRequest[strings.TrimSpace(fmt.Sprint(row["REQUEST"]))] = strings.TrimSpace(fmt.Sprint(row["RETCODE"]))
	}

	after, lerr := readLog(ctx, reqs)
	for _, r := range reqs {
		ir := ImportedRequest{Request: r, RetCode: perRequest[r]}
		if lerr == nil {
			for _, st := range after[r] {
				if !containsStep(before[r], st) {
					ir.Steps = append(ir.Steps, st)
					if st.RetCode > ir.MaxRC {
						ir.MaxRC = st.RetCode
					}
				}
			}
		}
		res.Requests = append(res.Requests, ir)
	}

	if res.RetCode != "000" {
		return res, fmt.Errorf("import into %s client %s failed: retcode %s (%s): %s",
			sid, client, res.RetCode, retCodeText(res.RetCode), res.Message)
	}
	res.Imported = true
	if lerr != nil {
		return res, fmt.Errorf("imported, but reading TPALOG afterwards failed: %w", lerr)
	}
	return res, nil
}

func containsStep(steps []saprfc.ImportStep, s saprfc.ImportStep) bool {
	for _, x := range steps {
		if x == s {
			return true
		}
	}
	return false
}

// retCodeText explains CTS_API_IMPORT_CHANGE_REQUEST's RETCODE.
func retCodeText(rc string) string {
	switch rc {
	case "000":
		return "imported"
	case "008":
		return "TMS could not start the import"
	case "009":
		return "tp finished with return code 8"
	case "010":
		return "tp finished with an error above 8"
	case "011":
		return "no authorization"
	case "012":
		return "TMS reported another error; see the TMS alert viewer"
	case "013":
		return "the import log could not be read"
	case "001", "002", "003", "004", "005", "006", "007":
		return "the target system or client was not accepted"
	}
	return "unknown code"
}
