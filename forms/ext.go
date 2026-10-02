// Package forms reads and writes print forms -- SAPscript forms, Smart Forms,
// Adobe forms and their interfaces -- through the form service, an ABAP class
// that runs in ZADT_VSP's WebSocket session (abap/ in this repository).
package forms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"github.com/gorynychzmey/vsp-extensions/internal/version"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/mcpext"
)

// Extension is the print form extension.
type Extension struct{}

// New returns the extension, for vsp.Run.
func New() Extension { return Extension{} }

func (Extension) Name() string    { return "forms" }
func (Extension) Version() string { return version.Version }

func (Extension) Actions() []mcpext.Action {
	var actions []mcpext.Action
	for _, t := range []string{FormTypeSmartForm, FormTypeSAPscript, FormTypeAdobeForm, FormTypeAdobeIntf} {
		actions = append(actions,
			mcpext.Action{Action: "read", Type: t, Class: mcpext.Read, Op: adt.OpRead, Handler: readHandler(t)},
			mcpext.Action{Action: "edit", Type: t, Class: mcpext.Mutate, Op: adt.OpUpdate, Handler: writeHandler(t, false)})
	}
	// Adobe forms and interfaces are changed, not created.
	for _, t := range []string{FormTypeSmartForm, FormTypeSAPscript} {
		actions = append(actions, mcpext.Action{Action: "create", Type: t, Class: mcpext.Mutate, Op: adt.OpCreate, Handler: writeHandler(t, true)})
	}
	return actions
}

func (Extension) Help() string {
	return `Print forms as documents, through the form service in ZADT_VSP:
  SAP(action="read", target="SSFO ZDEMO_SF")                    Smart Form, the SMARTFORMS download XML
  SAP(action="read", target="FORM ZDEMO_SCRIPT")                SAPscript form, abapGit's FORM structure, every language
  SAP(action="read", target="SFPF ZDEMO_PDF")                   Adobe form without its original-language layout
  SAP(action="read", target="SFPF ZDEMO_PDF", params={"language": "ES"})   the XDP layout of one language
  SAP(action="read", target="SFPI ZDEMO_IF")                    Adobe form interface
  params: "info": true (package, languages, inactive version), "file_path" (write the document to a file)

Write the document as read returns it, changed:
  SAP(action="edit", target="SFPF ZDEMO_PDF", params={"language": "ES", "file_path": "zdemo_es.xdp", "transport": "A4HK900001"})
  SAP(action="edit", target="SFPI ZDEMO_IF", params={"content": "<?xml ...", "test_run": true})
  Adobe forms and interfaces are activated afterwards; a replaced Adobe form that does not activate is put back.
  SAP(action="create", target="FORM ZDEMO_SCRIPT", params={"file_path": "zdemo.xml", "package": "$TMP"})   also SSFO`
}

func languageParam(params map[string]any) string {
	return mcpext.String(params, "language", "layout_language")
}

// bridge is the form service on env's WebSocket.
func bridge(ctx context.Context, env mcpext.Env, what string) (wsBridge, error) {
	ws, err := env.ZADTVSP(ctx)
	if err != nil {
		return wsBridge{}, fmt.Errorf("%s needs ZADT_VSP's form service (a WebSocket to the system): %w", what, err)
	}
	return wsBridge{ws: ws}, nil
}

// readHandler reads forms of one type; the core hands a handler the name
// only, so each type gets its own.
func readHandler(formType string) mcpext.Handler {
	return func(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
		return read(ctx, env, formType, name, params)
	}
}

func read(ctx context.Context, env mcpext.Env, formType, name string, params map[string]any) (*mcp.CallToolResult, error) {
	if name == "" {
		return mcpext.Errorf(`a form name is required: SAP(action="read", target="%s ZDEMO")`, formType), nil
	}
	b, err := bridge(ctx, env, "reading a form")
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	c := client{env.ADT()}
	if info, _ := mcpext.Bool(params, "info"); info {
		res, err := c.GetFormInfo(ctx, b, formType, name)
		if err != nil {
			return mcpext.Errorf("%s %s: %v", formType, name, err), nil
		}
		return mcpext.JSON(res), nil
	}
	form, err := c.ReadForm(ctx, b, formType, name, languageParam(params))
	if err != nil {
		return mcpext.Errorf("reading %s %s: %v", formType, name, err), nil
	}
	if path := mcpext.String(params, "file_path"); path != "" {
		if err := os.WriteFile(path, []byte(form.Content), 0o644); err != nil {
			return mcpext.Errorf("writing %s: %v", path, err), nil
		}
		return mcpext.JSON(map[string]any{
			"type": form.Type, "name": form.Name, "language": form.Language,
			"masterLanguage": form.MasterLanguage, "mimeType": form.MimeType,
			"file": path, "bytes": len(form.Content),
		}), nil
	}
	return mcpext.JSON(form), nil
}

// writeHandler writes forms of one type; create says the call was a create,
// which needs a package.
func writeHandler(formType string, create bool) mcpext.Handler {
	return func(ctx context.Context, env mcpext.Env, name string, params map[string]any) (*mcp.CallToolResult, error) {
		if create && mcpext.String(params, "package") == "" {
			return mcpext.Errorf(`creating %s %s needs a package: params={"package": "$TMP", ...}`, formType, name), nil
		}
		return write(ctx, env, formType, name, params)
	}
}

func write(ctx context.Context, env mcpext.Env, formType, name string, params map[string]any) (*mcp.CallToolResult, error) {
	opts := FormWriteOptions{
		Type:      formType,
		Name:      name,
		Language:  languageParam(params),
		Transport: mcpext.String(params, "transport", "request"),
		Package:   mcpext.String(params, "package"),
	}
	opts.TestRun, _ = mcpext.Bool(params, "test_run")
	if opts.Name == "" {
		return mcpext.Errorf(`a form name is required: SAP(action="edit", target="%s ZDEMO", params={"content": "..."})`, opts.Type), nil
	}
	opts.Content = mcpext.String(params, "content", "source")
	if path := mcpext.String(params, "file_path"); opts.Content == "" && path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return mcpext.Errorf("reading %s: %v", path, err), nil
		}
		opts.Content = string(data)
	}
	if opts.Content == "" {
		return mcpext.Errorf(`the form content is required: params={"content": "<?xml ..."} or {"file_path": "..."}, as SAP(action="read") returns it`), nil
	}
	b, err := bridge(ctx, env, "writing a form")
	if err != nil {
		return mcpext.Errorf("%v", err), nil
	}
	res, err := client{env.ADT()}.WriteForm(ctx, b, opts)
	if err != nil {
		msg := fmt.Sprintf("writing %s %s: %v", opts.Type, opts.Name, err)
		if res != nil {
			out, _ := json.MarshalIndent(res, "", "  ")
			msg += "\n" + string(out)
		}
		return mcpext.Errorf("%s", msg), nil
	}
	return mcpext.JSON(res), nil
}

// Commands adds `vsp form info|read|write`.
func (Extension) Commands(env mcpext.EnvFunc) []mcpext.Command {
	formCmd := &cobra.Command{
		Use:   "form",
		Short: "Read and write print forms: SAPscript, Smart Forms, Adobe forms (needs the form service)",
		Long: `Read and write print forms as documents, through the form service in ZADT_VSP.

  SSFO  Smart Form, the XML of the SMARTFORMS download
  FORM  SAPscript form, abapGit's FORM structure, every language
  SFPF  Adobe form without the layout of its original language;
        with --language, the XDP layout of that language
  SFPI  Adobe form interface

A write goes through the same checks as any other: allowed packages, allowed
transport requests, read-only mode. Adobe forms and interfaces are activated
after the write; a replaced Adobe form that does not activate is put back.

Examples:
  vsp form info SFPF ZDEMO_PDF
  vsp form read SFPF ZDEMO_PDF --language ES -o zdemo_es.xdp
  vsp form write SFPF ZDEMO_PDF --language ES -f zdemo_es.xdp --transport A4HK900001
  vsp form write FORM ZDEMO_SCRIPT -f zdemo.xml --package '$TMP'      # creates it
  vsp form write SSFO ZDEMO_SF -f zdemo_sf.xml --test-run`,
	}
	open := func(cmd *cobra.Command) (client, wsBridge, error) {
		e, err := env(cmd)
		if err != nil {
			return client{}, wsBridge{}, err
		}
		b, err := bridge(cmd.Context(), e, "a form")
		return client{e.ADT()}, b, err
	}
	infoCmd := &cobra.Command{
		Use:   "info <TYPE> <NAME>",
		Short: "Package, original language, languages and inactive version of a form",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, b, err := open(cmd)
			if err != nil {
				return err
			}
			info, err := c.GetFormInfo(cmd.Context(), b, args[0], args[1])
			if err != nil {
				return err
			}
			out, _ := json.MarshalIndent(info, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
	readCmd := &cobra.Command{
		Use:   "read <TYPE> <NAME>",
		Short: "Read a form as a document, to stdout or a file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			language, _ := cmd.Flags().GetString("language")
			outFile, _ := cmd.Flags().GetString("out")
			c, b, err := open(cmd)
			if err != nil {
				return err
			}
			form, err := c.ReadForm(cmd.Context(), b, args[0], args[1], language)
			if err != nil {
				return err
			}
			if outFile == "" {
				fmt.Print(form.Content)
				return nil
			}
			if err := os.WriteFile(outFile, []byte(form.Content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s %s -> %s (%d bytes, %s)\n", form.Type, form.Name, outFile, len(form.Content), form.MimeType)
			return nil
		},
	}
	writeCmd := &cobra.Command{
		Use:   "write <TYPE> <NAME>",
		Short: "Write a form from a document (a file, or stdin with -f -)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, _ := cmd.Flags().GetString("file")
			var data []byte
			var err error
			switch file {
			case "":
				return fmt.Errorf("--file is required (use - for stdin)")
			case "-":
				data, err = io.ReadAll(os.Stdin)
			default:
				data, err = os.ReadFile(file)
			}
			if err != nil {
				return err
			}
			opts := FormWriteOptions{Type: args[0], Name: args[1], Content: string(data)}
			opts.Language, _ = cmd.Flags().GetString("language")
			opts.Transport, _ = cmd.Flags().GetString("transport")
			opts.Package, _ = cmd.Flags().GetString("package")
			opts.TestRun, _ = cmd.Flags().GetBool("test-run")
			c, b, err := open(cmd)
			if err != nil {
				return err
			}
			res, err := c.WriteForm(cmd.Context(), b, opts)
			if res != nil {
				out, _ := json.MarshalIndent(res, "", "  ")
				fmt.Println(string(out))
			}
			return err
		},
	}
	readCmd.Flags().String("language", "", "Adobe form: read the XDP layout of this language (ISO code)")
	readCmd.Flags().StringP("out", "o", "", "Write the document to this file")
	writeCmd.Flags().StringP("file", "f", "", "The document to write (- for stdin)")
	writeCmd.Flags().String("language", "", "Adobe form: write the XDP layout of this language (ISO code)")
	writeCmd.Flags().String("transport", "", "Transport request (required for a transportable package)")
	writeCmd.Flags().String("package", "", "Package, to create a Smart Form or SAPscript form")
	writeCmd.Flags().Bool("test-run", false, "Check everything, save nothing")
	formCmd.AddCommand(infoCmd, readCmd, writeCmd)
	return []mcpext.Command{{Command: formCmd}}
}
