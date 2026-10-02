package sxci

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

// Extension is the classic BAdI implementation extension (SE19, SXCI).
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return "classic-badi" }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	return []mcpext.Action{
		{Action: "create", Type: "SXCI", Class: mcpext.Mutate, Op: adt.OpCreate, Handler: create},
		{Action: "delete", Type: "SXCI", Class: mcpext.Mutate, Op: adt.OpDelete, Handler: remove},
	}
}

func (Extension) Help() string {
	return `Classic BAdI implementation (SE19, SXCI), created and deleted as a background job over RFC, so it
needs the RFC connection. The implementing class is proposed from the name and generated like SE19 does:
  SAP(action="create", target="SXCI ZIMP_DEMO", params={"badi": "BADI_X", "description": "Demo",
      "package": "ZPKG", "transport": "A4HK900001", "filters": ["1000"]})
  "class" names the implementing class, "language" the original language (D or DE), "activate": false leaves it inactive.
  For a classic BAdI migrated to an enhancement spot, create an ENHO BAdI implementation instead.
  SAP(action="delete", target="SXCI ZIMP_DEMO", params={"transport": "A4HK900001"})
  deletes it with its class ("keep_class": true keeps the class).`
}

func nameOf(name string, params map[string]any) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return mcpext.String(params, "name")
}

func create(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	o := ClassicBadiImplementation{
		Name:        nameOf(name, params),
		Badi:        mcpext.String(params, "badi"),
		Class:       mcpext.String(params, "class"),
		Description: mcpext.String(params, "description"),
		Package:     mcpext.String(params, "package"),
		Transport:   mcpext.String(params, "transport"),
		Language:    strings.ToUpper(mcpext.String(params, "language")),
		Activate:    true,
	}
	if v, ok := mcpext.Bool(params, "activate"); ok {
		o.Activate = v
	}
	// One value or a list; a filter value may hold a comma, so a string is
	// never split.
	switch f := params["filters"].(type) {
	case nil:
	case string:
		o.Filters = []string{f}
	case []any:
		for _, v := range f {
			o.Filters = append(o.Filters, fmt.Sprint(v))
		}
	default:
		return mcpext.Errorf("filters must be a list of filter values"), nil
	}
	c := client{env.ADT()}
	return tempreport.Respond(ctx, env, "classic BAdI implementations", func(r tempreport.Runner) (tempreport.Outcome, error) {
		return outcome(c.CreateClassicBadiImplementation(ctx, o, r))
	})
}

func remove(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
	keep, _ := mcpext.Bool(params, "keep_class")
	c := client{env.ADT()}
	return tempreport.Respond(ctx, env, "classic BAdI implementations", func(r tempreport.Runner) (tempreport.Outcome, error) {
		return outcome(c.DeleteClassicBadiImplementation(ctx, nameOf(name, params), mcpext.String(params, "transport"), keep, r))
	})
}

func outcome(res *ClassicBadiResult, err error) (tempreport.Outcome, error) {
	if res == nil {
		return tempreport.Outcome{}, err
	}
	return tempreport.Outcome{Result: res, Program: res.Program, Warnings: res.Warnings}, err
}
