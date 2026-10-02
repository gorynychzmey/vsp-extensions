package trimport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// fakeExports stands in for rfc.Result.
type fakeExports struct {
	scalars map[string]any
	tables  map[string][]map[string]any
}

func (f fakeExports) Get(name string) any                { return f.scalars[name] }
func (f fakeExports) Table(name string) []map[string]any { return f.tables[name] }

// fakeSystem answers the function modules an import uses, with a TPALOG that
// gains rows when the import runs.
type fakeSystem struct {
	sid        string
	logBefore  []string // TPALOG rows as "TRKORR|TRCLI|TRSTEP|RETCODE|TRTIME"
	logAfter   []string
	importRC   string
	importMsg  string
	perRequest string
	calls      []string
	imported   bool
	importArgs rfc.Params
}

func (s *fakeSystem) call(_ context.Context, fm string, in rfc.Params) (exports, error) {
	s.calls = append(s.calls, fm)
	switch fm {
	case "RFC_SYSTEM_INFO":
		return fakeExports{scalars: map[string]any{"RFCSI_EXPORT": map[string]any{"RFCSYSID": s.sid}}}, nil
	case "CTS_API_IMPORT_CHANGE_REQUEST":
		s.imported = true
		s.importArgs = in
		var reqs []map[string]any
		for _, r := range in["REQUESTS"].([]map[string]any) {
			reqs = append(reqs, map[string]any{"REQUEST": r["REQUEST"], "RETCODE": s.perRequest})
		}
		return fakeExports{
			scalars: map[string]any{"RETCODE": s.importRC, "MESSAGE": s.importMsg},
			tables:  map[string][]map[string]any{"REQUESTS": reqs},
		}, nil
	}
	return nil, errors.New("unexpected " + fm)
}

func (s *fakeSystem) log(_ context.Context, reqs []string) (map[string][]saprfc.ImportStep, error) {
	s.calls = append(s.calls, "TPALOG")
	rows := s.logBefore
	if s.imported {
		rows = s.logAfter
	}
	out := map[string][]saprfc.ImportStep{}
	for _, r := range rows {
		f := strings.Split(r, "|")
		out[f[0]] = append(out[f[0]], saprfc.ImportStep{Client: f[1], Step: f[2], RetCode: f[3], Time: f[4]})
	}
	return out, nil
}

func TestImportRequests_ImportsIntoTheConnectedSystemAndReportsTheNewSteps(t *testing.T) {
	sys := &fakeSystem{
		sid: "QAS",
		// An earlier run of the same request must not be reported as this one.
		logBefore: []string{"TR-EXAMPLE|100|I|0000|20260101100000"},
		logAfter: []string{
			"TR-EXAMPLE|100|I|0000|20260101100000",
			"TR-EXAMPLE|ALL|L|0000|20260102100000",
			"TR-EXAMPLE|100|I|0004|20260102100005",
			"TR-EXAMPLE|ALL|G|0000|20260102100009",
		},
		importRC: "000", importMsg: "Request TR-EXAMPLE imported into system QAS client 100", perRequest: "000",
	}

	res, err := importRequests(context.Background(), sys.call, sys.log, []string{"tr-example"}, "100")
	if err != nil {
		t.Fatalf("importRequests: %v", err)
	}
	if got := sys.importArgs["SYSTEM"]; got != "QAS" {
		t.Errorf("SYSTEM = %v, want the connected system QAS", got)
	}
	if got := sys.importArgs["CLIENT"]; got != "100" {
		t.Errorf("CLIENT = %v, want 100", got)
	}
	if !res.Imported || res.System != "QAS" || len(res.Requests) != 1 {
		t.Fatalf("result = %+v", res)
	}
	r := res.Requests[0]
	if r.Request != "TR-EXAMPLE" || r.RetCode != "000" {
		t.Errorf("request = %+v", r)
	}
	if len(r.Steps) != 3 {
		t.Fatalf("steps = %+v, want the three of this run only", r.Steps)
	}
	if r.MaxRC != "0004" {
		t.Errorf("maxRC = %q, want 0004", r.MaxRC)
	}
}

func TestImportRequests_AFailedImportIsAnErrorWithTheSystemsMessage(t *testing.T) {
	sys := &fakeSystem{sid: "QAS", importRC: "012", importMsg: "Could not start import", perRequest: ""}

	res, err := importRequests(context.Background(), sys.call, sys.log, []string{"TR-EXAMPLE"}, "100")
	if err == nil {
		t.Fatal("a failed import was reported as success")
	}
	if !strings.Contains(err.Error(), "Could not start import") || !strings.Contains(err.Error(), "012") {
		t.Errorf("error lacks the system's code and message: %v", err)
	}
	if res == nil || res.Imported {
		t.Errorf("result = %+v, want Imported=false", res)
	}
}

func TestImportRequests_RefusesBeforeCalling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requests []string
		client   string
	}{
		{"no request", nil, "100"},
		{"no client", []string{"TR-EXAMPLE"}, ""},
		{"not a request number", []string{"X' OR '1'='1"}, "100"},
		{"bad client", []string{"TR-EXAMPLE"}, "1O0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sys := &fakeSystem{sid: "QAS", importRC: "000"}
			if _, err := importRequests(context.Background(), sys.call, sys.log, tc.requests, tc.client); err == nil {
				t.Fatal("accepted")
			}
			if len(sys.calls) != 0 {
				t.Errorf("called %v before refusing", sys.calls)
			}
		})
	}
}
