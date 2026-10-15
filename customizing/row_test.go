package customizing

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
	"github.com/oisee/vibing-steampunk/pkg/mcpext/mcpexttest"
)

// fakeDDIC answers the table reads of a change: DD02L, DD03L, E070 and the
// row itself.
type fakeDDIC struct {
	contflag, clidep string
	row              map[string]string
	e070             map[string]map[string]string
	reads            []string
	ran              bool
}

func (f *fakeDDIC) RunReport(context.Context, string) ([]string, error) {
	f.ran = true
	return []string{"VSP-ROW:TASK=TR-TASK", "VSP-ROW:OK="}, nil
}

func (f *fakeDDIC) ReadTable(_ context.Context, table, where string, _ []string, _ int) ([]map[string]string, error) {
	f.reads = append(f.reads, table+" "+where)
	switch table {
	case "DD02L":
		return []map[string]string{{"TABCLASS": "TRANSP", "CONTFLAG": f.contflag, "CLIDEP": f.clidep}}, nil
	case "DD03L":
		return []map[string]string{
			{"FIELDNAME": "MANDT", "POSITION": "0001", "KEYFLAG": "X", "INTTYPE": "C", "LENG": "000003", "DATATYPE": "CLNT"},
			{"FIELDNAME": "APPLIC", "POSITION": "0002", "KEYFLAG": "X", "INTTYPE": "C", "LENG": "000002", "DATATYPE": "CHAR"},
			{"FIELDNAME": "STEP", "POSITION": "0003", "KEYFLAG": "X", "INTTYPE": "C", "LENG": "000006", "DATATYPE": "CHAR"},
			{"FIELDNAME": ".INCLUDE", "POSITION": "0004", "KEYFLAG": "", "INTTYPE": "", "LENG": "000000", "DATATYPE": ""},
			{"FIELDNAME": "FMODUL", "POSITION": "0005", "KEYFLAG": "", "INTTYPE": "C", "LENG": "000030", "DATATYPE": "CHAR"},
			{"FIELDNAME": "PRMOD", "POSITION": "0006", "KEYFLAG": "", "INTTYPE": "N", "LENG": "000001", "DATATYPE": "NUMC"},
			{"FIELDNAME": "AMOUNT", "POSITION": "0007", "KEYFLAG": "", "INTTYPE": "P", "LENG": "000013", "DATATYPE": "CURR"},
		}, nil
	case "E070":
		for k, r := range f.e070 {
			if strings.Contains(where, "'"+k+"'") {
				return []map[string]string{r}, nil
			}
		}
		return nil, nil
	}
	if f.row == nil {
		return nil, nil
	}
	return []map[string]string{f.row}, nil
}

func validTable(t *testing.T) table {
	t.Helper()
	tab, err := readTableInfo(context.Background(), &fakeDDIC{contflag: "E", clidep: "X"}, "/SCWM/TSTEP_FLOW")
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestReadTableInfo_RefusesWhatIsNotClientCustomizing(t *testing.T) {
	for name, f := range map[string]*fakeDDIC{
		"application data (A)": {contflag: "A", clidep: "X"},
		"system table (S)":     {contflag: "S", clidep: "X"},
		"cross-client":         {contflag: "C", clidep: ""},
	} {
		if _, err := readTableInfo(context.Background(), f, "ZTAB"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidate(t *testing.T) {
	tab := validTable(t)
	key := map[string]string{"applic": "01", "STEP": "PAPHIN"}
	if k, s, err := validate(tab, RowEdit{Key: key, Set: map[string]string{"fmodul": "/PAR/X"}}); err != nil || len(k) != 2 || len(s) != 1 || s[0].Name != "FMODUL" {
		t.Fatalf("valid change: %v %v %v", k, s, err)
	}
	for name, e := range map[string]RowEdit{
		"key incomplete":          {Key: map[string]string{"APPLIC": "01"}, Set: map[string]string{"FMODUL": "X"}},
		"client given":            {Key: map[string]string{"MANDT": "200", "APPLIC": "01", "STEP": "S"}, Set: map[string]string{"FMODUL": "X"}},
		"unknown key field":       {Key: map[string]string{"APPLIC": "01", "STEP": "S", "XX": "1"}, Set: map[string]string{"FMODUL": "X"}},
		"key field in set":        {Key: key, Set: map[string]string{"STEP": "OTHER"}},
		"nothing to set":          {Key: key},
		"unknown field":           {Key: key, Set: map[string]string{"NOPE": "1"}},
		"too long":                {Key: key, Set: map[string]string{"FMODUL": strings.Repeat("X", 31)}},
		"not numeric":             {Key: key, Set: map[string]string{"PRMOD": "A"}},
		"packed field":            {Key: key, Set: map[string]string{"AMOUNT": "1.00"}},
		"line break":              {Key: key, Set: map[string]string{"FMODUL": "A\nB"}},
		"delimiter of the reader": {Key: key, Set: map[string]string{"FMODUL": "A|B"}},
	} {
		if _, _, err := validate(tab, e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTableKey_PadsEachFieldToItsLength(t *testing.T) {
	tab := validTable(t)
	key, err := validateKey(tab, map[string]string{"APPLIC": "1", "STEP": "AB"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tableKey("100", key, map[string]string{"APPLIC": "1", "STEP": "AB"})
	if err != nil || got != "1001 AB" {
		t.Errorf("tableKey = %q, %v", got, err)
	}
}

func TestRowEditSource(t *testing.T) {
	tab := validTable(t)
	key, set, err := validate(tab, RowEdit{Key: map[string]string{"APPLIC": "01", "STEP": "PAPH'N"}, Set: map[string]string{"FMODUL": "/PAR/RF_PACK_CREA_TO_PAI"}})
	if err != nil {
		t.Fatal(err)
	}
	src := rowEditSource("ZTEMP_TROW_1", "/SCWM/TSTEP_FLOW", key, map[string]string{"APPLIC": "01", "STEP": "PAPH'N"}, set,
		map[string]string{"FMODUL": "/SCWM/RF_PACK_CREA_TO_PAI"}, map[string]string{"FMODUL": "/PAR/RF_PACK_CREA_TO_PAI"}, "TR-REQ", "10001PAPH'N")
	for _, want := range []string{
		"REPORT ztemp_trow_1.",
		"gs_row   TYPE /SCWM/TSTEP_FLOW",
		"SELECT SINGLE * FROM /SCWM/TSTEP_FLOW INTO gs_row WHERE APPLIC = '01' AND STEP = 'PAPH''N'.",
		"IF gs_row-fmodul <> '/SCWM/RF_PACK_CREA_TO_PAI'.",
		"CALL FUNCTION 'ENQUEUE_E_TABLE'",
		"gs_row-fmodul = '/PAR/RF_PACK_CREA_TO_PAI'.",
		"PERFORM check_field USING '/SCWM/TSTEP_FLOW' 'FMODUL' gs_row-fmodul.",
		"iv_order = 'TR-REQ' iv_with_dialog = 'D'",
		"SELECT SINGLE trkorr FROM e071k INTO gv_found",
		"'the key is not in the task after recording'",
		"gs_e071k-tabkey = gv_varkey.",
		"UPDATE /SCWM/TSTEP_FLOW FROM gs_row.",
		"COMMIT WORK AND WAIT.",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("source lacks %q", want)
		}
	}
	// The recording, and reading it back, come before the update, so a key
	// that was not recorded changes nothing.
	if strings.Index(src, "FROM e071k") > strings.Index(src, "UPDATE /SCWM/TSTEP_FLOW") {
		t.Error("the row is updated before its recorded key is read back")
	}
	// ' ' is a check mode of TRINT_OBJECTS_CHECK_AND_INSERT: it records nothing.
	if strings.Contains(src, "iv_with_dialog = ' '") {
		t.Error("the recording runs in check mode")
	}
	for i, line := range strings.Split(src, "\n") {
		if len(line) > 255 {
			t.Errorf("line %d is %d characters long", i+1, len(line))
		}
	}
}

func TestRequestOf(t *testing.T) {
	f := &fakeDDIC{e070: map[string]map[string]string{
		"TR-W":    {"TRKORR": "TR-W", "TRFUNCTION": "W", "TRSTATUS": "D"},
		"TR-TASK": {"TRKORR": "TR-TASK", "TRFUNCTION": "Q", "TRSTATUS": "D", "STRKORR": "TR-W"},
		"TR-K":    {"TRKORR": "TR-K", "TRFUNCTION": "K", "TRSTATUS": "D"},
		"TR-REL":  {"TRKORR": "TR-REL", "TRFUNCTION": "W", "TRSTATUS": "R"},
	}}
	for in, want := range map[string]string{"TR-W": "TR-W", "TR-TASK": "TR-W"} {
		if got, err := requestOf(context.Background(), f, in); err != nil || got != want {
			t.Errorf("requestOf(%s) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"TR-K", "TR-REL", "TR-NONE"} {
		if _, err := requestOf(context.Background(), f, in); err == nil {
			t.Errorf("requestOf(%s) accepted", in)
		}
	}
}

// A row that already holds the values is not touched: no report runs.
func TestEditRow_UnchangedRowRunsNothing(t *testing.T) {
	f := &fakeDDIC{contflag: "C", clidep: "X", row: map[string]string{"FMODUL": "/PAR/X"},
		e070: map[string]map[string]string{"D11K900001": {"TRKORR": "D11K900001", "TRFUNCTION": "W", "TRSTATUS": "D"}}}
	res, err := EditRow(context.Background(), nil, f, "100", RowEdit{Table: "ZTAB", Transport: "D11K900001",
		Key: map[string]string{"APPLIC": "01", "STEP": "S"}, Set: map[string]string{"FMODUL": "/PAR/X"}})
	if err != nil || !res.Unchanged || f.ran {
		t.Errorf("res = %+v, err = %v, ran = %v", res, err, f.ran)
	}
}

func TestApplyRowLog(t *testing.T) {
	res := &RowResult{}
	if err := applyRowLog(res, []string{"VSP-ROW:ORDER=TR-W", "VSP-ROW:TASK=TR-TASK", "VSP-ROW:OK="}); err != nil || res.Task != "TR-TASK" {
		t.Errorf("ok log: %+v %v", res, err)
	}
	if err := applyRowLog(&RowResult{}, []string{"VSP-ROW:ERR=Recording the key in the request: key in SAP namespace"}); err == nil || !strings.Contains(err.Error(), "SAP namespace") {
		t.Errorf("err log: %v", err)
	}
	if err := applyRowLog(&RowResult{}, []string{"job cancelled"}); err == nil {
		t.Error("a log without OK passed")
	}
}

func envWith(safety adt.SafetyConfig, settings map[string]any) *mcpexttest.Env {
	c := adt.NewClient("http://127.0.0.1:1", "", "", adt.WithSafety(safety))
	return &mcpexttest.Env{Client: c, Settings: settings, SystemInfo: mcpext.System{Client: "100"}}
}

func TestExtensionIsWellBehaved(t *testing.T) {
	mcpexttest.Check(t, New())
}

// The gates before any RFC: the table list, a request, transports enabled,
// the request whitelist, transport_read_only.
func TestHandleEditGates(t *testing.T) {
	on := adt.SafetyConfig{EnableTransports: true, AllowedTransports: []string{"D11K*"}}
	tro := on
	tro.TransportReadOnly = true
	tables := map[string]any{"tables": []any{"/SCWM/TSTEP_FLOW"}}
	ok := map[string]any{"key": map[string]any{"APPLIC": "01"}, "set": map[string]any{"FMODUL": "X"}, "transport": "D11K900001"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, tc := range map[string]struct {
		safety   adt.SafetyConfig
		settings map[string]any
		target   string
		params   map[string]any
		want     string
	}{
		"no table list":       {on, nil, "/SCWM/TSTEP_FLOW", ok, "off for this system"},
		"table not listed":    {on, tables, "T000", ok, "not in the tables"},
		"no request":          {on, tables, "/SCWM/TSTEP_FLOW", with("transport", ""), "customizing request is required"},
		"transports off":      {adt.SafetyConfig{}, tables, "/SCWM/TSTEP_FLOW", ok, "not enabled"},
		"outside whitelist":   {on, tables, "/SCWM/TSTEP_FLOW", with("transport", "A4HK900001"), "allowed"},
		"transport read-only": {tro, tables, "/SCWM/TSTEP_FLOW", ok, "transport read-only"},
		"value not a string":  {on, tables, "/SCWM/TSTEP_FLOW", with("set", map[string]any{"PRMOD": 1}), "must be a string"},
	} {
		res, err := handleEdit(context.Background(), envWith(tc.safety, tc.settings), tc.target, tc.params)
		if err != nil || res == nil || !res.IsError {
			t.Errorf("%s: %+v %v", name, res, err)
			continue
		}
		if text := resultText(res); !strings.Contains(text, tc.want) {
			t.Errorf("%s: %q, want one naming %q", name, text, tc.want)
		}
	}
}

func resultText(res *mcp.CallToolResult) string {
	text, _ := res.Content[0].(mcp.TextContent)
	return text.Text
}
