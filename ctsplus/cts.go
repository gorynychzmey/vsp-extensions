package ctsplus

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// A non-ABAP request (CTS+) belongs to its source system -- D22K... for a PI
// system D22 -- but lives in the domain controller's Transport Organizer. The
// classic request function modules refuse it there ("Incorrect system or
// wrong naming convention"): TR_ASSIGN_REQUEST_TO_PROJECT, TR40_SET_ATTRIBUTE,
// TMW_ADD_TR_ATTRIBUTES. The CTS_WBO_* function modules go through the CTS
// request factory instead, as the CTS Organizer Web UI does, and take it:
//
//	CTS_WBO_GET_REQUEST_DETAILS  header, CTS project, attributes
//	CTS_WBO_MODIFY_REQUEST       CTS project (SETPROJECT)
//	CTS_WBO_RELEASE_REQUEST      release and export
//	SCTS_API_IMP_GET_IMP_QUEUE   import queue of a system of the domain
//	SCTS_API_IMPORT_TRANSP_ITEMS import into a system of the domain
//
// All are remote-enabled and run on the domain controller itself.

// exports is what a call returns; rfc.Result is one.
type exports interface {
	Get(name string) any
	Table(name string) []map[string]any
}

type callFn func(ctx context.Context, fm string, in rfc.Params) (exports, error)

// logFn reads the TPALOG steps of requests, by request, oldest first.
type logFn func(ctx context.Context, requests []string) (map[string][]saprfc.ImportStep, error)

func clientCall(c *rfc.Client) callFn {
	return func(ctx context.Context, fm string, in rfc.Params) (exports, error) { return c.Call(ctx, fm, in) }
}

func clientLog(c *rfc.Client) logFn {
	return func(ctx context.Context, reqs []string) (map[string][]saprfc.ImportStep, error) {
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
}

var (
	requestPattern = regexp.MustCompile(`^[A-Z0-9]{3}K[A-Z0-9]{6}$`)
	projectPattern = regexp.MustCompile(`^[A-Z0-9_/]{1,20}$`)
	systemPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9]{2}$`)
	domainPattern  = regexp.MustCompile(`^[A-Z0-9_]{1,10}$`)
)

func normalizedRequest(r string) (string, error) {
	r = strings.ToUpper(strings.TrimSpace(r))
	if !requestPattern.MatchString(r) {
		return "", fmt.Errorf("%q is not a request number", r)
	}
	return r, nil
}

// Request is a request as the Transport Organizer of the domain controller
// knows it.
type Request struct {
	Request     string            `json:"request"`
	Description string            `json:"description"`
	Owner       string            `json:"owner"`
	State       string            `json:"state"` // CHANGEABLE, RELEASED, ...
	Type        string            `json:"type"`
	Target      string            `json:"target"`
	Project     string            `json:"project,omitempty"`
	ExtProject  string            `json:"extProject,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// Released reports whether the request is no longer changeable.
func (r *Request) Released() bool { return r.State == "RELEASED" }

func str(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// ctsError reads the error a CTS_WBO_* call reports in its exports: an
// EXCEPTION name, an error RETURNCODE, ERRORDETAIL.
func ctsError(fm string, out exports) error {
	exc := str(out.Get("EXCEPTION"))
	var typ, msg string
	if rc, ok := out.Get("RETURNCODE").(map[string]any); ok {
		typ, msg = str(rc["TYPE"]), str(rc["MESSAGE"])
	}
	detail := str(out.Get("ERRORDETAIL"))
	if exc == "" && typ != "E" && typ != "A" && typ != "X" {
		return nil
	}
	parts := []string{}
	for _, p := range []string{exc, msg, detail} {
		if p != "" && !contains(parts, p) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "error "+typ)
	}
	return fmt.Errorf("%s: %s", fm, strings.Join(parts, ": "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// details reads a request through CTS_WBO_GET_REQUEST_DETAILS.
func details(ctx context.Context, call callFn, req string) (*Request, error) {
	out, err := call(ctx, "CTS_WBO_GET_REQUEST_DETAILS", rfc.Params{"REQUESTID": req, "ATTRIBUTESREQUIRED": "X"})
	if err != nil {
		return nil, fmt.Errorf("CTS_WBO_GET_REQUEST_DETAILS: %w", err)
	}
	if err := ctsError("CTS_WBO_GET_REQUEST_DETAILS", out); err != nil {
		return nil, err
	}
	d, _ := out.Get("REQUEST_DETAIL").(map[string]any)
	r := &Request{
		Request:     str(d["REQUEST"]),
		Description: str(d["DESCRIPTION"]),
		Owner:       str(d["OWNER"]),
		State:       str(d["STATE"]),
		Type:        str(d["TRTYPE"]),
		Target:      str(d["TARSYSTEM"]),
		Project:     str(d["PROJECTID"]),
		ExtProject:  str(out.Get("EXTPROJECT")),
	}
	if r.Request == "" {
		return nil, fmt.Errorf("request %s does not exist", req)
	}
	for _, a := range out.Table("ATTRIBUTES") {
		if r.Attributes == nil {
			r.Attributes = map[string]string{}
		}
		r.Attributes[str(a["REQ_ATTR_NAME"])] = str(a["REQ_ATTR_VALUE"])
	}
	return r, nil
}

// setProject assigns the CTS project and reads the request back: an
// assignment that did not take must not pass for one that did.
func setProject(ctx context.Context, call callFn, req, project string) (*Request, error) {
	out, err := call(ctx, "CTS_WBO_MODIFY_REQUEST", rfc.Params{
		"REQUEST":    req,
		"PROJECT":    project,
		"SETPROJECT": "X",
		// SETDESCRIPTION defaults to 'X' and would blank the description.
		"SETDESCRIPTION": "",
		"SETOWNER":       "",
	})
	if err != nil {
		return nil, fmt.Errorf("CTS_WBO_MODIFY_REQUEST: %w", err)
	}
	if err := ctsError("CTS_WBO_MODIFY_REQUEST", out); err != nil {
		return nil, err
	}
	r, err := details(ctx, call, req)
	if err != nil {
		return nil, err
	}
	if r.Project != project {
		return r, fmt.Errorf("CTS_WBO_MODIFY_REQUEST reported success, but %s has project %q, not %q", req, r.Project, project)
	}
	return r, nil
}

// SetProject assigns a changeable request to a CTS project.
func SetProject(ctx context.Context, c *rfc.Client, req, project string) (*Request, error) {
	return setProjectChecked(ctx, clientCall(c), req, project)
}

func setProjectChecked(ctx context.Context, call callFn, req, project string) (*Request, error) {
	project = strings.ToUpper(strings.TrimSpace(project))
	if !projectPattern.MatchString(project) {
		return nil, fmt.Errorf("%q is not a CTS project", project)
	}
	r, err := details(ctx, call, req)
	if err != nil {
		return nil, err
	}
	if r.Released() {
		return r, fmt.Errorf("%s is already released", req)
	}
	if r.Project == project {
		return r, nil
	}
	return setProject(ctx, call, req, project)
}

// Release releases a request and so exports it. A request without a CTS
// project is refused by CTS when the domain makes the project mandatory;
// project assigns one first.
func Release(ctx context.Context, c *rfc.Client, req, project string) (*Request, error) {
	return release(ctx, clientCall(c), req, project)
}

func release(ctx context.Context, call callFn, req, project string) (*Request, error) {
	r, err := details(ctx, call, req)
	if err != nil {
		return nil, err
	}
	if r.Released() {
		return r, fmt.Errorf("%s is already released", req)
	}
	if project != "" && r.Project != strings.ToUpper(project) {
		if r, err = setProjectChecked(ctx, call, req, project); err != nil {
			return r, err
		}
	}
	out, err := call(ctx, "CTS_WBO_RELEASE_REQUEST", rfc.Params{"REQUEST": req, "CLOSEONLY": "", "USESTRATEGY": ""})
	if err != nil {
		return r, fmt.Errorf("CTS_WBO_RELEASE_REQUEST: %w", err)
	}
	if err := ctsError("CTS_WBO_RELEASE_REQUEST", out); err != nil {
		if r.Project == "" {
			err = fmt.Errorf("%w (the request has no CTS project; pass \"project\")", err)
		}
		return r, err
	}
	after, err := details(ctx, call, req)
	if err != nil {
		return r, fmt.Errorf("released, but reading %s back failed: %w", req, err)
	}
	if !after.Released() {
		return after, fmt.Errorf("CTS_WBO_RELEASE_REQUEST reported success, but %s is %s", req, after.State)
	}
	return after, nil
}

// QueueItem is one request in a system's import queue.
type QueueItem struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Status      string   `json:"status"` // as SCTS_API reports it
	Type        string   `json:"type,omitempty"`
	Projects    []string `json:"projects,omitempty"`
}

func systemParam(system, domain string) map[string]any {
	return map[string]any{"NAME": system, "DOMAIN": domain}
}

// Queue reads a system's import queue.
func Queue(ctx context.Context, c *rfc.Client, system, domain string) ([]QueueItem, error) {
	return queue(ctx, clientCall(c), system, domain)
}

func queue(ctx context.Context, call callFn, system, domain string) ([]QueueItem, error) {
	out, err := call(ctx, "SCTS_API_IMP_GET_IMP_QUEUE", rfc.Params{"SYSTEM": systemParam(system, domain)})
	if err != nil {
		return nil, fmt.Errorf("SCTS_API_IMP_GET_IMP_QUEUE: %w", err)
	}
	var items []QueueItem
	for _, row := range out.Table("TRANSPORT_ITEMS") {
		it := QueueItem{
			ID:          str(row["ID"]),
			Description: str(row["DESCRIPTION"]),
			Owner:       str(row["OWNER"]),
			Status:      str(row["STATUS"]),
			Type:        str(row["TYPE"]),
		}
		if ps, ok := row["PROJECTS"].([]any); ok {
			for _, p := range ps {
				if m, ok := p.(map[string]any); ok && str(m["ID"]) != "" {
					it.Projects = append(it.Projects, str(m["ID"]))
				}
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// ImportResult is what an import into a system of the domain did.
type ImportResult struct {
	System   string            `json:"system"`
	Domain   string            `json:"domain,omitempty"`
	Imported bool              `json:"imported"`
	Requests []ImportedRequest `json:"requests"`
	Message  string            `json:"message,omitempty"`
}

// ImportedRequest is one request of an import, with the TPALOG steps the
// import added for the target system and the worst return code among them.
type ImportedRequest struct {
	Request string              `json:"request"`
	Steps   []saprfc.ImportStep `json:"steps,omitempty"`
	MaxRC   string              `json:"maxRc,omitempty"`
}

// Import imports requests into a system of the domain, from the domain
// controller, as STMS does for a non-ABAP system.
func Import(ctx context.Context, c *rfc.Client, reqs []string, system, domain string) (*ImportResult, error) {
	return importItems(ctx, clientCall(c), clientLog(c), reqs, system, domain)
}

func importItems(ctx context.Context, call callFn, readLog logFn, reqs []string, system, domain string) (*ImportResult, error) {
	before, err := readLog(ctx, reqs)
	if err != nil {
		return nil, fmt.Errorf("reading TPALOG before the import: %w", err)
	}
	out, err := call(ctx, "SCTS_API_IMPORT_TRANSP_ITEMS", rfc.Params{
		"TRANSPORT_ITEM_IDS": reqs,
		"IMPORT_SYSTEM":      systemParam(system, domain),
	})
	if err != nil {
		return nil, fmt.Errorf("SCTS_API_IMPORT_TRANSP_ITEMS: %w", err)
	}
	res := &ImportResult{System: system, Domain: domain}
	if alert, ok := out.Get("ALERT").(map[string]any); ok {
		res.Message = str(alert["MESSAGE"])
		if rc := str(alert["RETURN_CODE"]); rc != "" && rc != "0" {
			id := strings.TrimSpace(str(alert["MESSAGE_ID"]) + " " + str(alert["MESSAGE_NO"]))
			return res, fmt.Errorf("import into %s refused: return code %s %s: %s", system, rc, id, res.Message)
		}
	}
	res.Imported = true

	after, lerr := readLog(ctx, reqs)
	for _, r := range reqs {
		ir := ImportedRequest{Request: r}
		if lerr == nil {
			for _, st := range after[r] {
				if st.Target != "" && st.Target != system {
					continue
				}
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
	if lerr != nil {
		return res, fmt.Errorf("imported, but reading TPALOG afterwards failed: %w", lerr)
	}
	for _, ir := range res.Requests {
		if ir.MaxRC >= "0008" {
			return res, fmt.Errorf("%s imported into %s with return code %s", ir.Request, system, ir.MaxRC)
		}
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
