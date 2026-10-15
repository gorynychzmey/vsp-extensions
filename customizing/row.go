package customizing

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gorynychzmey/vsp-extensions/internal/tempreport"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// A row of a customizing table is changed the way SM30 changes it, without
// its screens: the key is recorded in a customizing request (R3TR TABU with
// the key in E071K) and the row is updated in the same LUW, under the table
// lock SM30 takes. Neither the recording (TRINT_OBJECTS_CHECK_AND_INSERT) nor
// the lock is remote-enabled, so the steps run as a temporary report in a
// background job.

const rowMarker = "VSP-ROW:"

// field is a column of the table, from DD03L.
type field struct {
	Name    string
	Key     bool
	IntType string // ABAP type: C, N, D, T, ...
	Length  int    // characters
	Client  bool
}

// table is what the change needs to know of the table.
type table struct {
	Name     string
	ContFlag string // delivery class
	Fields   []field
}

func (t table) field(name string) (field, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return field{}, false
}

// keyFields are the key fields after the client, in order.
func (t table) keyFields() []field {
	var out []field
	for _, f := range t.Fields {
		if f.Key && !f.Client {
			out = append(out, f)
		}
	}
	return out
}

// RowEdit is one change of one row.
type RowEdit struct {
	Table     string
	Key       map[string]string
	Set       map[string]string
	Transport string
}

// RowResult is what a change did.
type RowResult struct {
	Table     string            `json:"table"`
	Key       map[string]string `json:"key"`
	Before    map[string]string `json:"before"`
	After     map[string]string `json:"after,omitempty"`
	Changed   []string          `json:"changed,omitempty"`
	Unchanged bool              `json:"unchanged,omitempty"`
	Request   string            `json:"request,omitempty"`
	Task      string            `json:"task,omitempty"`
	TableKey  string            `json:"tableKey,omitempty"`
	Program   string            `json:"program,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
}

// customizingClasses are the delivery classes whose rows are customizing:
// customer customizing (C), protected customizing (G), and control tables
// with separate key areas for SAP and customer (E), where CTS refuses a key
// in SAP's area when the key is recorded.
var customizingClasses = map[string]bool{"C": true, "G": true, "E": true}

// charTypes are the ABAP types a value is given in as it is stored.
var charTypes = map[string]bool{"C": true, "N": true, "D": true, "T": true}

func readTableInfo(ctx context.Context, run tempreport.Runner, name string) (table, error) {
	t := table{Name: name}
	rows, err := run.ReadTable(ctx, "DD02L", fmt.Sprintf("TABNAME = '%s' AND AS4LOCAL = 'A'", name),
		[]string{"TABCLASS", "CONTFLAG", "CLIDEP"}, 1)
	if err != nil {
		return t, fmt.Errorf("reading DD02L: %w", err)
	}
	if len(rows) == 0 {
		return t, fmt.Errorf("table %s does not exist or is not active", name)
	}
	if rows[0]["TABCLASS"] != "TRANSP" {
		return t, fmt.Errorf("%s is not a transparent table (%s)", name, rows[0]["TABCLASS"])
	}
	t.ContFlag = rows[0]["CONTFLAG"]
	if !customizingClasses[t.ContFlag] {
		return t, fmt.Errorf("%s has delivery class %q; only customizing tables (C, G, E) are changed here", name, t.ContFlag)
	}
	if rows[0]["CLIDEP"] != "X" {
		return t, fmt.Errorf("%s is cross-client; only client-specific customizing is changed here", name)
	}
	cols, err := run.ReadTable(ctx, "DD03L", fmt.Sprintf("TABNAME = '%s' AND AS4LOCAL = 'A'", name),
		[]string{"FIELDNAME", "POSITION", "KEYFLAG", "INTTYPE", "LENG", "DATATYPE"}, 0)
	if err != nil {
		return t, fmt.Errorf("reading DD03L: %w", err)
	}
	sort.SliceStable(cols, func(i, j int) bool { return cols[i]["POSITION"] < cols[j]["POSITION"] })
	for _, c := range cols {
		n := c["FIELDNAME"]
		if n == "" || strings.HasPrefix(n, ".") {
			continue // include markers; their fields are listed themselves
		}
		var l int
		_, _ = fmt.Sscan(strings.TrimLeft(c["LENG"], "0"), &l)
		t.Fields = append(t.Fields, field{
			Name: n, Key: c["KEYFLAG"] == "X", IntType: c["INTTYPE"], Length: l,
			Client: c["DATATYPE"] == "CLNT",
		})
	}
	if len(t.Fields) == 0 || !t.Fields[0].Client {
		return t, fmt.Errorf("%s does not start with a client field", name)
	}
	return t, nil
}

// checkValue refuses a value that would not be stored as given.
func checkValue(f field, v string) error {
	if strings.ContainsAny(v, "\r\n|") {
		return fmt.Errorf("%s: line breaks and | are not allowed", f.Name)
	}
	if n := utf8.RuneCountInString(v); n > f.Length {
		return fmt.Errorf("%s holds %d characters, %q has %d", f.Name, f.Length, v, n)
	}
	digits := func() bool { return strings.Trim(v, "0123456789") == "" }
	switch f.IntType {
	case "N":
		if !digits() {
			return fmt.Errorf("%s is numeric text; %q is not", f.Name, v)
		}
	case "D", "T":
		if v != "" && (!digits() || len(v) != f.Length) {
			return fmt.Errorf("%s takes %d digits (internal format), got %q", f.Name, f.Length, v)
		}
	}
	return nil
}

// validateKey checks a full key against the table and returns its fields in
// table order.
func validateKey(t table, keyValues map[string]string) ([]field, error) {
	k := upperKeys(keyValues)
	var key []field
	for _, f := range t.keyFields() {
		v, ok := k[f.Name]
		if !ok {
			return nil, fmt.Errorf("the key lacks %s; give every key field of %s but the client", f.Name, t.Name)
		}
		if !charTypes[f.IntType] {
			return nil, fmt.Errorf("key field %s has ABAP type %s; only character-like keys are supported", f.Name, f.IntType)
		}
		if err := checkValue(f, v); err != nil {
			return nil, err
		}
		key = append(key, f)
		delete(k, f.Name)
	}
	for n := range k {
		if f, ok := t.field(n); ok && f.Client {
			return nil, fmt.Errorf("the client is the server's own; do not give %s", n)
		}
		return nil, fmt.Errorf("%s is not a key field of %s", n, t.Name)
	}
	return key, nil
}

// validate checks the change against the table and returns the key and the
// fields to set, in table order.
func validate(t table, e RowEdit) (key, set []field, err error) {
	if key, err = validateKey(t, e.Key); err != nil {
		return nil, nil, err
	}
	s := upperKeys(e.Set)
	if len(s) == 0 {
		return nil, nil, fmt.Errorf(`nothing to change: name the fields in "set"`)
	}
	for _, f := range t.Fields {
		v, ok := s[f.Name]
		if !ok {
			continue
		}
		switch {
		case f.Key:
			return nil, nil, fmt.Errorf("%s is a key field; a changed key is another row", f.Name)
		case !charTypes[f.IntType]:
			return nil, nil, fmt.Errorf("%s has ABAP type %s; only character-like fields are supported", f.Name, f.IntType)
		}
		if err := checkValue(f, v); err != nil {
			return nil, nil, err
		}
		set = append(set, f)
		delete(s, f.Name)
	}
	for n := range s {
		return nil, nil, fmt.Errorf("%s is not a field of %s", n, t.Name)
	}
	return key, set, nil
}

// tableKey is the row's key as E071K and the table lock take it: the client
// and every key field, each padded to its length.
func tableKey(client string, key []field, values map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString(client)
	for _, f := range key {
		v := values[f.Name]
		b.WriteString(v)
		b.WriteString(strings.Repeat(" ", f.Length-utf8.RuneCountInString(v)))
	}
	k := strings.TrimRight(b.String(), " ")
	if utf8.RuneCountInString(k) > 120 {
		return "", fmt.Errorf("the key %q is longer than the 120 characters a request entry holds", k)
	}
	return k, nil
}

func whereClause(key []field, values map[string]string) string {
	parts := make([]string, 0, len(key))
	for _, f := range key {
		parts = append(parts, fmt.Sprintf("%s = %s", f.Name, tempreport.Literal(values[f.Name])))
	}
	return strings.Join(parts, " AND ")
}

// requestOf finds the modifiable customizing request a transport names: the
// request itself, or the request of a task.
func requestOf(ctx context.Context, run tempreport.Runner, transport string) (string, error) {
	rows, err := run.ReadTable(ctx, "E070", fmt.Sprintf("TRKORR = '%s'", transport),
		[]string{"TRKORR", "TRFUNCTION", "TRSTATUS", "STRKORR"}, 1)
	if err != nil {
		return "", fmt.Errorf("reading E070: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("request %s does not exist", transport)
	}
	r := rows[0]
	if r["STRKORR"] != "" {
		return requestOf(ctx, run, r["STRKORR"])
	}
	if r["TRFUNCTION"] != "W" {
		return "", fmt.Errorf("%s is not a customizing request (type %s); create one with "+
			`SAP(action="system", params={"type": "create_transport", "request_type": "customizing", ...})`, transport, r["TRFUNCTION"])
	}
	if r["TRSTATUS"] != "D" {
		return "", fmt.Errorf("%s is not modifiable (status %s)", transport, r["TRSTATUS"])
	}
	return r["TRKORR"], nil
}

// ReadRow reads one row of a table by its full key.
func ReadRow(ctx context.Context, run tempreport.Runner, name string, keyValues map[string]string) (map[string]string, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if err := tempreport.ValidateName("table", name, 30); err != nil {
		return nil, err
	}
	t, err := readTableInfo(ctx, run, name)
	if err != nil {
		return nil, err
	}
	key, err := validateKey(t, keyValues)
	if err != nil {
		return nil, err
	}
	values := upperKeys(keyValues)
	var cols []string
	for _, f := range t.Fields {
		if !f.Client {
			cols = append(cols, f.Name)
		}
	}
	rows, err := run.ReadTable(ctx, name, whereClause(key, values), cols, 2)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("%s has no row with this key", name)
	}
	return rows[0], nil
}

func upperKeys(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	return out
}

// EditRow changes fields of one existing row and records its key in a
// customizing request.
func EditRow(ctx context.Context, c *adt.Client, run tempreport.Runner, client string, e RowEdit) (*RowResult, error) {
	e.Table = strings.ToUpper(strings.TrimSpace(e.Table))
	if err := tempreport.ValidateName("table", e.Table, 30); err != nil {
		return nil, err
	}
	e.Transport = strings.ToUpper(strings.TrimSpace(e.Transport))
	if err := tempreport.ValidateName("transport", e.Transport, 20); err != nil {
		return nil, err
	}
	t, err := readTableInfo(ctx, run, e.Table)
	if err != nil {
		return nil, err
	}
	key, set, err := validate(t, e)
	if err != nil {
		return nil, err
	}
	keyValues, setValues := upperKeys(e.Key), upperKeys(e.Set)
	request, err := requestOf(ctx, run, e.Transport)
	if err != nil {
		return nil, err
	}
	tabkey, err := tableKey(client, key, keyValues)
	if err != nil {
		return nil, err
	}

	cols := make([]string, 0, len(set))
	for _, f := range set {
		cols = append(cols, f.Name)
	}
	rows, err := run.ReadTable(ctx, e.Table, whereClause(key, keyValues), cols, 2)
	if err != nil {
		return nil, fmt.Errorf("reading the row of %s: %w", e.Table, err)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("%s has no row with this key; only existing rows are changed", e.Table)
	}
	res := &RowResult{Table: e.Table, Key: keyValues, Before: rows[0], Request: request, TableKey: tabkey}
	var changed []field
	for _, f := range set {
		if strings.TrimRight(rows[0][f.Name], " ") != strings.TrimRight(setValues[f.Name], " ") {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		res.Unchanged = true
		return res, nil
	}

	tr, err := tempreport.Run(ctx, c, "ZTEMP_TROW_", func(prog string) string {
		return rowEditSource(prog, e.Table, key, keyValues, changed, rows[0], setValues, request, tabkey)
	}, run)
	res.Program = tr.Program
	res.Warnings = append(res.Warnings, tr.Warnings...)
	if err != nil {
		return res, err
	}
	if err := applyRowLog(res, tr.Lines); err != nil {
		return res, err
	}
	res.After = map[string]string{}
	for k, v := range res.Before {
		res.After[k] = v
	}
	for _, f := range changed {
		res.After[f.Name] = setValues[f.Name]
		res.Changed = append(res.Changed, f.Name)
	}
	return res, nil
}

func applyRowLog(res *RowResult, lines []string) error {
	done := false
	var failure string
	for _, line := range lines {
		i := strings.Index(line, rowMarker)
		if i < 0 {
			continue
		}
		k, v, _ := strings.Cut(strings.TrimSpace(line[i+len(rowMarker):]), "=")
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "OK":
			done = true
		case "ERR":
			failure = v
		case "TASK":
			res.Task = v
		case "ORDER":
			if v != "" {
				res.Request = v
			}
		}
	}
	if failure != "" {
		return fmt.Errorf("%s", failure)
	}
	if !done {
		tail := lines
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		return fmt.Errorf("the report %s ended without reporting success; job log: %s", res.Program, strings.Join(tail, " | "))
	}
	return nil
}

// rowEditSource is the report: read the row, refuse it if it changed since
// it was read, lock it as SM30 does, check the new values against their
// check tables and fixed values, record the key in the request, update,
// commit. A failure before the commit rolls everything back.
func rowEditSource(prog, tab string, key []field, keyValues map[string]string, set []field,
	before, values map[string]string, request, tabkey string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "REPORT %s.\n\n", strings.ToLower(prog))
	fmt.Fprintf(&b, `DATA: gs_row   TYPE %s,
      gt_ko200 TYPE tredt_objects,
      gs_ko200 LIKE LINE OF gt_ko200,
      gt_e071k TYPE tredt_keys,
      gs_e071k LIKE LINE OF gt_e071k,
      gv_order TYPE trkorr,
      gv_task  TYPE trkorr,
      gv_varkey TYPE rstable-varkey.
`, tab)
	b.WriteString(rowCommon)
	b.WriteString("\nSTART-OF-SELECTION.\n")
	fmt.Fprintf(&b, "  SELECT SINGLE * FROM %s INTO gs_row WHERE %s.\n", tab, whereClause(key, keyValues))
	b.WriteString("  IF sy-subrc <> 0.\n    PERFORM fail_text USING 'Reading the row' 'the row does not exist'.\n  ENDIF.\n")
	for _, f := range set {
		fmt.Fprintf(&b, "  IF gs_row-%s <> %s.\n    PERFORM fail_text USING 'Reading the row' 'it was changed since vsp read it; read it again'.\n  ENDIF.\n",
			strings.ToLower(f.Name), tempreport.Literal(before[f.Name]))
	}
	fmt.Fprintf(&b, "  gv_varkey = %s.\n", tempreport.Literal(tabkey))
	fmt.Fprintf(&b, `  CALL FUNCTION 'ENQUEUE_E_TABLE'
    EXPORTING mode_rstable = 'E' tabname = %s varkey = gv_varkey
    EXCEPTIONS foreign_lock = 1 system_failure = 2 OTHERS = 3.
  IF sy-subrc <> 0.
    PERFORM fail USING 'Locking the row (ENQUEUE_E_TABLE)'.
  ENDIF.
`, tempreport.Literal(tab))
	for _, f := range set {
		fmt.Fprintf(&b, "  gs_row-%s = %s.\n", strings.ToLower(f.Name), tempreport.Literal(values[f.Name]))
	}
	for _, f := range set {
		fmt.Fprintf(&b, "  PERFORM check_field USING %s %s gs_row-%s.\n",
			tempreport.Literal(tab), tempreport.Literal(f.Name), strings.ToLower(f.Name))
	}
	fmt.Fprintf(&b, `  gs_ko200-pgmid = 'R3TR'. gs_ko200-object = 'TABU'. gs_ko200-obj_name = %[1]s. gs_ko200-objfunc = 'K'.
  APPEND gs_ko200 TO gt_ko200.
  gs_e071k-pgmid = 'R3TR'. gs_e071k-object = 'TABU'. gs_e071k-objname = %[1]s.
  gs_e071k-mastertype = 'TABU'. gs_e071k-mastername = %[1]s. gs_e071k-tabkey = gv_varkey.
  APPEND gs_e071k TO gt_e071k.
  CALL FUNCTION 'TRINT_OBJECTS_CHECK_AND_INSERT'
    EXPORTING iv_order = %[2]s iv_with_dialog = ' ' iv_send_message = ' '
    IMPORTING ev_order = gv_order ev_task = gv_task
    CHANGING ct_ko200 = gt_ko200 ct_e071k = gt_e071k
    EXCEPTIONS OTHERS = 1.
  IF sy-subrc <> 0.
    PERFORM fail USING 'Recording the key in the request (TRINT_OBJECTS_CHECK_AND_INSERT)'.
  ENDIF.
  UPDATE %[3]s FROM gs_row.
  IF sy-subrc <> 0 OR sy-dbcnt <> 1.
    PERFORM fail_text USING 'Updating the row' 'no row was updated'.
  ENDIF.
  COMMIT WORK AND WAIT.
  CALL FUNCTION 'DEQUEUE_E_TABLE' EXPORTING mode_rstable = 'E' tabname = %[1]s varkey = gv_varkey.
  PERFORM say USING 'ORDER' gv_order.
  PERFORM say USING 'TASK' gv_task.
  PERFORM say USING 'OK' ''.
`, tempreport.Literal(tab), tempreport.Literal(request), tab)
	return b.String()
}

const rowCommon = `
FORM say USING iv_key TYPE csequence iv_value TYPE csequence.
  DATA lv_text TYPE string.
  CONCATENATE 'VSP-ROW:' iv_key '=' iv_value INTO lv_text.
  MESSAGE lv_text TYPE 'S'.
ENDFORM.

* The value is checked as a screen field of the table would be: fixed values
* of its domain and its check table, with the rest of the row as context.
FORM check_field USING iv_tab TYPE csequence iv_field TYPE csequence iv_value TYPE any.
  DATA: lv_tab   TYPE dfies-tabname,
        lv_field TYPE dfies-lfieldname,
        lv_msgid TYPE sy-msgid, lv_msgty TYPE sy-msgty, lv_msgno TYPE sy-msgno,
        lv_v1 TYPE sy-msgv1, lv_v2 TYPE sy-msgv2, lv_v3 TYPE sy-msgv3, lv_v4 TYPE sy-msgv4,
        lv_msg TYPE string.
  lv_tab = iv_tab. lv_field = iv_field.
  CALL FUNCTION 'DDUT_INPUT_CHECK'
    EXPORTING tabname = lv_tab fieldname = lv_field value = iv_value value_list = gs_row
    IMPORTING msgid = lv_msgid msgty = lv_msgty msgno = lv_msgno
              msgv1 = lv_v1 msgv2 = lv_v2 msgv3 = lv_v3 msgv4 = lv_v4
    EXCEPTIONS no_ddic_field = 1 illegal_move = 2 OTHERS = 3.
  IF sy-subrc <> 0.
    PERFORM fail USING 'Checking the new value (DDUT_INPUT_CHECK)'.
  ENDIF.
  IF lv_msgty CA 'EAX'.
    MESSAGE ID lv_msgid TYPE 'S' NUMBER lv_msgno WITH lv_v1 lv_v2 lv_v3 lv_v4 INTO lv_msg.
    lv_msg = |{ iv_field }: { lv_msg } ({ lv_msgid } { lv_msgno })|.
    PERFORM fail_text USING 'Checking the new value' lv_msg.
  ENDIF.
ENDFORM.

FORM fail USING iv_step TYPE csequence.
  DATA: lv_msg  TYPE string,
        lv_code TYPE string.
  IF sy-msgid IS INITIAL.
    lv_code = sy-subrc.
    CONCATENATE 'sy-subrc' lv_code INTO lv_msg SEPARATED BY space.
  ELSE.
    MESSAGE ID sy-msgid TYPE 'S' NUMBER sy-msgno
            WITH sy-msgv1 sy-msgv2 sy-msgv3 sy-msgv4 INTO lv_msg.
    lv_msg = |{ lv_msg } ({ sy-msgid } { sy-msgno })|.
  ENDIF.
  PERFORM fail_text USING iv_step lv_msg.
ENDFORM.

FORM fail_text USING iv_step TYPE csequence iv_text TYPE csequence.
  DATA lv_msg TYPE string.
  lv_msg = |{ iv_step }: { iv_text }|.
  ROLLBACK WORK.
  CALL FUNCTION 'DEQUEUE_ALL'.
  PERFORM say USING 'ERR' lv_msg.
  LEAVE PROGRAM.
ENDFORM.
`
