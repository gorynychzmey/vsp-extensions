package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --profile <name> starts vspx on a system of ~/.vsp.json, as a launcher
// script did before: it sets the logon (SAP_URL, SAP_USER, SAP_CLIENT,
// SAP_PASSWORD, SAP_SYSTEM, SAP_LANGUAGE) and turns the system's safety
// settings into the server's flags, so an MCP client can start vspx directly.
// The vsp server takes its logon from flags and SAP_* only; the profile is
// read here, before vsp sees the command line, and the logon still reaches
// vsp the way it expects it.

// profileSystem is what a system entry of .vsp.json holds for the server. It
// reads keys vsp itself does not know: sysid, mode, disallowed_ops.
type profileSystem struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Client   string `json:"client"`
	Language string `json:"language"`
	SysID    string `json:"sysid"`
	Mode     string `json:"mode"`

	ReadOnly                bool     `json:"read_only"`
	Insecure                bool     `json:"insecure"`
	EnableTransports        bool     `json:"enable_transports"`
	AllowTransportableEdits bool     `json:"allow_transportable_edits"`
	TransportReadOnly       bool     `json:"transport_read_only"`
	AllowedPackages         []string `json:"allowed_packages"`
	AllowedTransports       []string `json:"allowed_transports"`
	TransportChoice         string   `json:"transport_choice"`
	CTSProject              string   `json:"cts_project"`
	TransportTarget         string   `json:"transport_target"`
	DisallowedOps           string   `json:"disallowed_ops"`
}

type profileFile struct {
	Systems map[string]profileSystem `json:"systems"`
}

// cutProfile takes --profile <name> or --profile=<name> out of args.
func cutProfile(args []string) (name string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--profile":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", nil, fmt.Errorf("--profile needs the name of a system in .vsp.json")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--profile="):
			name = strings.TrimPrefix(a, "--profile=")
		default:
			rest = append(rest, a)
		}
	}
	return name, rest, nil
}

// passwordNames are the environment variables a system's password is looked
// up in, most specific first: one password per system and client, then one
// per system, then vsp's own name-based variable.
func passwordNames(name string, sys profileSystem) []string {
	sid := strings.ToUpper(strings.TrimSpace(sys.SysID))
	var out []string
	if sid != "" && sys.Client != "" {
		out = append(out, fmt.Sprintf("VSP_%s_%s_PASSWORD", sid, sys.Client))
	}
	if sid != "" {
		out = append(out, fmt.Sprintf("VSP_%s_PASSWORD", sid))
	}
	if n := fmt.Sprintf("VSP_%s_PASSWORD", strings.ToUpper(name)); !contains(out, n) {
		out = append(out, n)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// resolveProfile returns the environment and the flags that start the server
// on the named system. getenv looks a variable up (the process environment,
// then the user's stored one, so a password set after the client started is
// found).
func resolveProfile(name string, data []byte, getenv func(string) string) (env map[string]string, flags []string, err error) {
	var f profileFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("reading .vsp.json: %w", err)
	}
	sys, ok := f.Systems[name]
	if !ok {
		return nil, nil, fmt.Errorf("system %q is not in .vsp.json", name)
	}
	if sys.URL == "" || sys.User == "" || sys.Client == "" {
		return nil, nil, fmt.Errorf("system %q in .vsp.json needs url, user and client", name)
	}
	names := passwordNames(name, sys)
	password := ""
	for _, n := range names {
		if password = getenv(n); password != "" {
			break
		}
	}
	if password == "" {
		return nil, nil, fmt.Errorf("no password for system %q: set one of %s", name, strings.Join(names, ", "))
	}
	env = map[string]string{
		"SAP_URL":      sys.URL,
		"SAP_USER":     sys.User,
		"SAP_CLIENT":   sys.Client,
		"SAP_PASSWORD": password,
		"SAP_SYSTEM":   name,
	}
	if sys.Language != "" {
		env["SAP_LANGUAGE"] = sys.Language
	}

	str := func(flag, v string) {
		if v != "" {
			flags = append(flags, flag, v)
		}
	}
	list := func(flag string, v []string) {
		if len(v) > 0 {
			flags = append(flags, flag, strings.Join(v, ","))
		}
	}
	on := func(flag string, v bool) {
		if v {
			flags = append(flags, flag)
		}
	}
	str("--mode", sys.Mode)
	on("--read-only", sys.ReadOnly)
	on("--insecure", sys.Insecure)
	on("--enable-transports", sys.EnableTransports)
	on("--allow-transportable-edits", sys.AllowTransportableEdits)
	list("--allowed-packages", sys.AllowedPackages)
	list("--allowed-transports", sys.AllowedTransports)
	str("--transport-choice", sys.TransportChoice)
	str("--cts-project", sys.CTSProject)
	str("--transport-target", sys.TransportTarget)
	on("--transport-read-only", sys.TransportReadOnly)
	str("--disallowed-ops", sys.DisallowedOps)
	return env, flags, nil
}

// applyProfile handles --profile on the command line: it sets the logon in
// the environment and puts the system's flags before the caller's own, so a
// flag given on the command line still wins.
func applyProfile() error {
	name, rest, err := cutProfile(os.Args[1:])
	if err != nil || name == "" {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(home, ".vsp.json"))
	if err != nil {
		return fmt.Errorf("--profile %s: %w", name, err)
	}
	env, flags, err := resolveProfile(name, data, lookupEnv)
	if err != nil {
		return fmt.Errorf("--profile %s: %w", name, err)
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	os.Args = append(append([]string{os.Args[0]}, flags...), rest...)
	return nil
}
