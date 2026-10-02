package idoc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gorynychzmey/vsp-extensions/internal/tempreport"
	"github.com/gorynychzmey/vsp-extensions/internal/version"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
)

// Extension is the IDoc development extension: segment types (WE31, SEGM)
// and extensions of basic types (WE30, IEXT).
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return "idoc-types" }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	return []mcpext.Action{
		{Action: "read", Type: "SEGM", Class: mcpext.Read, Op: adt.OpRead, Handler: readSegment},
		{Action: "create", Type: "SEGM", Class: mcpext.Mutate, Op: adt.OpCreate, Handler: createSegment},
		{Action: "edit", Type: "SEGM", Class: mcpext.Mutate, Op: adt.OpUpdate, Handler: changeSegment},
		{Action: "delete", Type: "SEGM", Class: mcpext.Mutate, Op: adt.OpDelete, Handler: deleteSegment},
		{Action: "read", Type: "IEXT", Class: mcpext.Read, Op: adt.OpRead, Handler: readExtension},
		{Action: "create", Type: "IEXT", Class: mcpext.Mutate, Op: adt.OpCreate, Handler: createExtension},
		{Action: "edit", Type: "IEXT", Class: mcpext.Mutate, Op: adt.OpUpdate, Handler: changeExtension},
		{Action: "delete", Type: "IEXT", Class: mcpext.Mutate, Op: adt.OpDelete, Handler: deleteExtension},
	}
}

func (Extension) Help() string {
	return `IDoc segment types (WE31) and extensions (WE30). Reading goes over RFC_READ_TABLE; every change runs
as a background job over RFC, and SAP records the objects in the request itself (R3TR TABL with
TDAT EDISEGMENT, R3TR IEXT).

  SAP(action="read", target="SEGM Z1DEMO")      fields, versions, release state
  SAP(action="read", target="IEXT ZDELVRY07")   basic type, added segments, release state

  SAP(action="create", target="SEGM Z1DEMO", params={"description": "Demo segment", "package": "ZPKG",
      "transport": "A4HK900001", "fields": [{"name": "MATNR", "data_element": "MATNR"}, "FLAG CHAR1"], "release": true})
  "qualified": true makes it a qualified segment; "iso_code": true on a field converts it to its ISO code.

  "fields" is the complete new list; fields of a released version must stay, in order -- new ones go at
  the end. Released in this SAP release, the version is reopened, changed and released again; released
  in an older one, a new version is added:
  SAP(action="edit", target="SEGM Z1DEMO", params={"fields": ["MATNR MATNR", "FLAG CHAR1", "STAMP TIMESTAMP"], "transport": "A4HK900001"})
  SAP(action="edit", target="SEGM Z1DEMO", params={"release": true, "transport": "A4HK900001"})   release; false reopens
  SAP(action="delete", target="SEGM Z1DEMO", params={"transport": "A4HK900001"})   with all its versions

  SAP(action="create", target="IEXT ZDELVRY07", params={"basic_type": "DELVRY07", "description": "Demo extension",
      "package": "ZPKG", "transport": "A4HK900001", "segments": [{"segment": "Z1DEMO", "parent": "E1EDL20", "min": 1, "max": 1},
      {"segment": "Z1DEMOSUB", "parent": "Z1DEMO", "max": 99}], "release": true})
  A parent is a segment of the basic type or one the extension adds before it; "mandatory": true, min/max default 1.

  "segments" is the complete new list; a released extension keeps its segments as they are and takes new
  ones. Only an extension released in this SAP release is changed or reopened -- an earlier release is
  refused (note 844899):
  SAP(action="edit", target="IEXT ZDELVRY07", params={"segments": [...], "description": "...", "transport": "A4HK900001"})
  SAP(action="edit", target="IEXT ZDELVRY07", params={"release": false, "transport": "A4HK900001"})   cancel the release
  SAP(action="delete", target="IEXT ZDELVRY07", params={"transport": "A4HK900001"})   released in this release: cancelled first`
}

func nameOf(name string, params map[string]any) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return mcpext.String(params, "name")
}

func respond(ctx context.Context, env mcpext.Env, what string, fn func(client, tempreport.Runner) (tempreport.Outcome, error)) (*mcp.CallToolResult, error) {
	c := client{env.ADT()}
	return tempreport.Respond(ctx, env, what, func(r tempreport.Runner) (tempreport.Outcome, error) { return fn(c, r) })
}

// list reads a list param that may also arrive as a JSON-encoded string.
func list(params map[string]any, key, what string) ([]any, bool, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	if s, isString := raw.(string); isString {
		var decoded []any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, true, fmt.Errorf("%s must be a list: %v", key, err)
		}
		raw = decoded
	}
	l, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("%s must be a list of %s", key, what)
	}
	return l, true, nil
}

// segmentFields reads "fields": a list of {"name", "data_element",
// "iso_code"} objects, or of "NAME DATA_ELEMENT" strings.
func segmentFields(params map[string]any) ([]SegmentField, bool, error) {
	l, given, err := list(params, "fields", `{"name", "data_element"} objects`)
	if err != nil || !given {
		return nil, given, err
	}
	fields := make([]SegmentField, 0, len(l))
	for i, item := range l {
		switch v := item.(type) {
		case string:
			name, dtel, found := strings.Cut(strings.TrimSpace(v), " ")
			if !found {
				return nil, true, fmt.Errorf("field %d %q: write it as \"NAME DATA_ELEMENT\"", i+1, v)
			}
			fields = append(fields, SegmentField{Name: name, DataElement: strings.TrimSpace(dtel)})
		case map[string]any:
			f := SegmentField{Name: fmt.Sprint(v["name"]), DataElement: fmt.Sprint(v["data_element"])}
			if v["data_element"] == nil {
				f.DataElement = fmt.Sprint(v["rollname"])
			}
			if iso, ok := v["iso_code"].(bool); ok {
				f.ISOCode = iso
			}
			fields = append(fields, f)
		default:
			return nil, true, fmt.Errorf("field %d is neither an object nor a string", i+1)
		}
	}
	return fields, true, nil
}

// extensionSegments reads "segments": a list of {"segment", "parent", "min",
// "max", "mandatory"} objects, or of "SEGMENT PARENT" strings (1..1).
func extensionSegments(params map[string]any) ([]ExtensionSegment, bool, error) {
	l, given, err := list(params, "segments", `{"segment", "parent"} objects`)
	if err != nil || !given {
		return nil, given, err
	}
	segs := make([]ExtensionSegment, 0, len(l))
	for i, item := range l {
		switch v := item.(type) {
		case string:
			seg, parent, found := strings.Cut(strings.TrimSpace(v), " ")
			if !found {
				return nil, true, fmt.Errorf("segment %d %q: write it as \"SEGMENT PARENT\"", i+1, v)
			}
			segs = append(segs, ExtensionSegment{Segment: seg, Parent: strings.TrimSpace(parent)})
		case map[string]any:
			s := ExtensionSegment{}
			s.Segment, _ = v["segment"].(string)
			s.Parent, _ = v["parent"].(string)
			if n, ok := v["min"].(float64); ok {
				s.Min = int64(n)
			}
			if n, ok := v["max"].(float64); ok {
				s.Max = int64(n)
			}
			s.Mandatory, _ = v["mandatory"].(bool)
			if s.Segment == "" || s.Parent == "" {
				return nil, true, fmt.Errorf("segment %d needs \"segment\" and \"parent\"", i+1)
			}
			segs = append(segs, s)
		default:
			return nil, true, fmt.Errorf("segment %d is neither an object nor a string", i+1)
		}
	}
	return segs, true, nil
}

func segmentOutcome(res *SegmentResult, err error) (tempreport.Outcome, error) {
	if res == nil {
		return tempreport.Outcome{}, err
	}
	return tempreport.Outcome{Result: res, Program: res.Program, Warnings: res.Warnings}, err
}

func extensionOutcome(res *ExtensionResult, err error) (tempreport.Outcome, error) {
	if res == nil {
		return tempreport.Outcome{}, err
	}
	return tempreport.Outcome{Result: res, Program: res.Program, Warnings: res.Warnings}, err
}

func readSegment(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	return respond(ctx, env, "IDoc segments", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		seg, err := c.ReadIDocSegment(ctx, nameOf(name, params), r)
		return tempreport.Outcome{Result: seg}, err
	})
}

func createSegment(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	fields, _, err := segmentFields(params)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	o := SegmentCreate{
		Name:        nameOf(name, params),
		Description: mcpext.String(params, "description"),
		Package:     mcpext.String(params, "package"),
		Transport:   mcpext.String(params, "transport"),
		Fields:      fields,
	}
	o.Qualified, _ = mcpext.Bool(params, "qualified")
	o.Release, _ = mcpext.Bool(params, "release")
	return respond(ctx, env, "IDoc segments", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return segmentOutcome(c.CreateIDocSegment(ctx, o, r))
	})
}

func changeSegment(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	fields, given, err := segmentFields(params)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	o := SegmentChange{Name: nameOf(name, params), Transport: mcpext.String(params, "transport")}
	if given {
		o.Fields = fields
		if o.Fields == nil {
			o.Fields = []SegmentField{}
		}
	}
	if v, ok := mcpext.Bool(params, "release"); ok {
		o.Release = &v
	}
	return respond(ctx, env, "IDoc segments", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return segmentOutcome(c.ChangeIDocSegment(ctx, o, r))
	})
}

func deleteSegment(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	return respond(ctx, env, "IDoc segments", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return segmentOutcome(c.DeleteIDocSegment(ctx, nameOf(name, params), mcpext.String(params, "transport"), r))
	})
}

func readExtension(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	return respond(ctx, env, "IDoc extensions", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		ext, err := c.ReadIDocExtension(ctx, nameOf(name, params), r)
		return tempreport.Outcome{Result: ext}, err
	})
}

func createExtension(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	segs, _, err := extensionSegments(params)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	o := ExtensionCreate{
		Name:        nameOf(name, params),
		BasicType:   mcpext.String(params, "basic_type", "idoctype"),
		Description: mcpext.String(params, "description"),
		Package:     mcpext.String(params, "package"),
		Transport:   mcpext.String(params, "transport"),
		Segments:    segs,
	}
	o.Release, _ = mcpext.Bool(params, "release")
	return respond(ctx, env, "IDoc extensions", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return extensionOutcome(c.CreateIDocExtension(ctx, o, r))
	})
}

func changeExtension(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	segs, given, err := extensionSegments(params)
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	o := ExtensionChange{
		Name:        nameOf(name, params),
		Transport:   mcpext.String(params, "transport"),
		Description: mcpext.String(params, "description"),
	}
	if given {
		o.Segments = segs
		if o.Segments == nil {
			o.Segments = []ExtensionSegment{}
		}
	}
	if v, ok := mcpext.Bool(params, "release"); ok {
		o.Release = &v
	}
	return respond(ctx, env, "IDoc extensions", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return extensionOutcome(c.ChangeIDocExtension(ctx, o, r))
	})
}

func deleteExtension(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	return respond(ctx, env, "IDoc extensions", func(c client, r tempreport.Runner) (tempreport.Outcome, error) {
		return extensionOutcome(c.DeleteIDocExtension(ctx, nameOf(name, params), mcpext.String(params, "transport"), r))
	})
}
