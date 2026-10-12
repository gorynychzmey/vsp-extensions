package ctsplus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

type fakeExports struct {
	scalars map[string]any
	tables  map[string][]map[string]any
}

func (f fakeExports) Get(name string) any                { return f.scalars[name] }
func (f fakeExports) Table(name string) []map[string]any { return f.tables[name] }

// fakeOrganizer is the domain controller's Transport Organizer for one
// non-ABAP request, with what CTS does to it.
type fakeOrganizer struct {
	state, project, description string
	// projectMandatory refuses a release without a project, as DOMAIN_D11 does.
	projectMandatory bool
	// modifyIgnored answers CTS_WBO_MODIFY_REQUEST with success and changes
	// nothing, as CTS_WBO_MODIFY_REQUEST_ATTRS did.
	modifyIgnored bool
	importAlert   map[string]any
	logBefore     []saprfc.ImportStep
	logAfter      []saprfc.ImportStep
	imported      bool
	calls         []string
	args          map[string]rfc.Params
}

func (o *fakeOrganizer) call(_ context.Context, fm string, in rfc.Params) (exports, error) {
	o.calls = append(o.calls, fm)
	if o.args == nil {
		o.args = map[string]rfc.Params{}
	}
	o.args[fm] = in
	ok := map[string]any{"TYPE": "", "MESSAGE": ""}
	switch fm {
	case "RFC_SYSTEM_INFO":
		return fakeExports{scalars: map[string]any{"RFCSI_EXPORT": map[string]any{"RFCSYSID": "D11"}}}, nil
	case "CTS_WBO_GET_REQUEST_DETAILS":
		return fakeExports{scalars: map[string]any{
			"RETURNCODE": ok,
			"EXTPROJECT": "SAP_OP",
			"REQUEST_DETAIL": map[string]any{"REQUEST": in["REQUESTID"], "STATE": o.state, "PROJECTID": o.project,
				"DESCRIPTION": o.description, "TARSYSTEM": "Q22", "TRTYPE": "K", "OWNER": "DEV"},
		}, tables: map[string][]map[string]any{"ATTRIBUTES": {{"REQ_ATTR_NAME": "EXPTIMESTAMP", "REQ_ATTR_VALUE": "20261012133923"}}}}, nil
	case "CTS_WBO_MODIFY_REQUEST":
		if !o.modifyIgnored && in["SETPROJECT"] == "X" {
			o.project, _ = in["PROJECT"].(string)
		}
		if in["SETDESCRIPTION"] != "" {
			o.description = ""
		}
		return fakeExports{scalars: map[string]any{"RETURNCODE": ok}}, nil
	case "CTS_WBO_RELEASE_REQUEST":
		if o.projectMandatory && o.project == "" {
			return fakeExports{scalars: map[string]any{"RETURNCODE": map[string]any{"TYPE": "E",
				"MESSAGE": "Request attribute SAP_CTS_PROJECT is mandatory"}}}, nil
		}
		o.state = "RELEASED"
		return fakeExports{scalars: map[string]any{"RETURNCODE": ok, "RC": "0"}}, nil
	case "SCTS_API_IMP_GET_IMP_QUEUE":
		return fakeExports{tables: map[string][]map[string]any{"TRANSPORT_ITEMS": {
			{"ID": "D22K90023Z", "STATUS": "X", "PROJECTS": []any{map[string]any{"ID": "D11_P00005"}}},
		}}}, nil
	case "SCTS_API_IMPORT_TRANSP_ITEMS":
		o.imported = true
		alert := o.importAlert
		if alert == nil {
			alert = map[string]any{"RETURN_CODE": 0, "MESSAGE": ""}
		}
		return fakeExports{scalars: map[string]any{"ALERT": alert}}, nil
	}
	return nil, errors.New("unexpected " + fm)
}

func (o *fakeOrganizer) log(_ context.Context, reqs []string) (map[string][]saprfc.ImportStep, error) {
	steps := o.logBefore
	if o.imported {
		steps = o.logAfter
	}
	return map[string][]saprfc.ImportStep{reqs[0]: steps}, nil
}

func (o *fakeOrganizer) called(fm string) bool {
	for _, c := range o.calls {
		if c == fm {
			return true
		}
	}
	return false
}

// The case that started it: a PI export leaves the request without a
// project, and CTS refuses its release. release assigns the project first,
// keeps the description, and confirms the request is released.
func TestRelease_AssignsTheProjectFirst(t *testing.T) {
	o := &fakeOrganizer{state: "CHANGEABLE", description: "VSP_CTSPLUS_TEST", projectMandatory: true}
	r, err := release(context.Background(), o.call, "D22K900240", "d11_p00005")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Released() || r.Project != "D11_P00005" || r.Description != "VSP_CTSPLUS_TEST" {
		t.Errorf("released = %+v", r)
	}
	if got := o.args["CTS_WBO_MODIFY_REQUEST"]["SETDESCRIPTION"]; got != "" {
		t.Errorf("SETDESCRIPTION = %q, want it cleared", got)
	}
}

func TestRelease_WithoutProjectNamesTheMissingProject(t *testing.T) {
	o := &fakeOrganizer{state: "CHANGEABLE", projectMandatory: true}
	_, err := release(context.Background(), o.call, "D22K900240", "")
	if err == nil || !strings.Contains(err.Error(), "SAP_CTS_PROJECT is mandatory") || !strings.Contains(err.Error(), `pass "project"`) {
		t.Errorf("err = %v", err)
	}
}

func TestRelease_AlreadyReleasedIsRefusedBeforeAnyChange(t *testing.T) {
	o := &fakeOrganizer{state: "RELEASED", project: "D11_P00005"}
	_, err := release(context.Background(), o.call, "D22K900240", "")
	if err == nil || !strings.Contains(err.Error(), "already released") || o.called("CTS_WBO_RELEASE_REQUEST") {
		t.Errorf("err = %v, calls = %v", err, o.calls)
	}
}

// A modify that answers success and changes nothing must not pass.
func TestSetProject_ReadsTheRequestBack(t *testing.T) {
	o := &fakeOrganizer{state: "CHANGEABLE", modifyIgnored: true}
	_, err := setProjectChecked(context.Background(), o.call, "D22K900240", "D11_P00005")
	if err == nil || !strings.Contains(err.Error(), "reported success") {
		t.Errorf("err = %v", err)
	}
}

func TestSetProject_RefusesWhatIsNotAProject(t *testing.T) {
	o := &fakeOrganizer{state: "CHANGEABLE"}
	if _, err := setProjectChecked(context.Background(), o.call, "D22K900240", "X' OR '1'='1"); err == nil || len(o.calls) != 0 {
		t.Errorf("err = %v, calls = %v", err, o.calls)
	}
}

func TestImport_ReportsTheStepsOfTheTargetOnly(t *testing.T) {
	o := &fakeOrganizer{
		logBefore: []saprfc.ImportStep{{Step: "E", RetCode: "0000", Time: "20261012153923", Target: "D22"}},
		logAfter: []saprfc.ImportStep{
			{Step: "E", RetCode: "0000", Time: "20261012153923", Target: "D22"},
			{Step: "I", RetCode: "0000", Time: "20261012154041", Target: "Q22"},
			{Step: "T", RetCode: "0004", Time: "20261012154050", Target: "Q22"},
		},
	}
	res, err := importItems(context.Background(), o.call, o.log, []string{"D22K900240"}, "Q22", "")
	if err != nil {
		t.Fatal(err)
	}
	ir := res.Requests[0]
	if !res.Imported || len(ir.Steps) != 2 || ir.MaxRC != "0004" {
		t.Errorf("result = %+v", res)
	}
	sys, _ := o.args["SCTS_API_IMPORT_TRANSP_ITEMS"]["IMPORT_SYSTEM"].(map[string]any)
	if sys["NAME"] != "Q22" {
		t.Errorf("IMPORT_SYSTEM = %v", sys)
	}
}

func TestImport_AlertIsAnError(t *testing.T) {
	o := &fakeOrganizer{importAlert: map[string]any{"RETURN_CODE": 8, "MESSAGE": "not in queue", "MESSAGE_ID": "SCTS_API", "MESSAGE_NO": "012"}}
	res, err := importItems(context.Background(), o.call, o.log, []string{"D22K900240"}, "Q22", "")
	if err == nil || !strings.Contains(err.Error(), "not in queue") || res.Imported {
		t.Errorf("err = %v, res = %+v", err, res)
	}
}

func TestImport_ReturnCode8IsAnError(t *testing.T) {
	o := &fakeOrganizer{logAfter: []saprfc.ImportStep{{Step: "I", RetCode: "0008", Time: "20261012154041", Target: "Q22"}}}
	_, err := importItems(context.Background(), o.call, o.log, []string{"D22K900240"}, "Q22", "")
	if err == nil || !strings.Contains(err.Error(), "0008") {
		t.Errorf("err = %v", err)
	}
}

func TestQueue_ReadsProjects(t *testing.T) {
	o := &fakeOrganizer{}
	items, err := queue(context.Background(), o.call, "Q22", "")
	if err != nil || len(items) != 1 || items[0].Status != "X" || len(items[0].Projects) != 1 {
		t.Errorf("items = %+v, err = %v", items, err)
	}
}

func TestRefuseOwnRequest(t *testing.T) {
	o := &fakeOrganizer{}
	if err := refuseOwnRequest(context.Background(), o.call, "D11K900001"); err == nil {
		t.Error("an ABAP request of the controller itself was taken")
	}
	if err := refuseOwnRequest(context.Background(), o.call, "D22K900240"); err != nil {
		t.Errorf("non-ABAP request refused: %v", err)
	}
}
