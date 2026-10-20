package main

import (
	"strings"
	"testing"
)

const profileJSON = `{"systems": {
  "d11": {"sysid": "D11", "url": "https://d11.example:44314", "user": "dev", "client": "100", "language": "DE",
          "mode": "hyperfocused", "enable_transports": true, "allowed_packages": ["Z*", "$TMP"],
          "allowed_transports": ["D11K*", "D22K*"], "cts_project": "D11_P00005", "transport_target": "/PAR/"},
  "q11-200": {"sysid": "Q11", "url": "https://q11.example", "user": "dev", "client": "200",
          "enable_transports": true, "transport_read_only": true, "disallowed_ops": "CDUAWLT"},
  "nosid": {"url": "https://x.example", "user": "dev", "client": "001"}
}}`

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestCutProfile(t *testing.T) {
	for _, args := range [][]string{{"--profile", "d11", "--verbose"}, {"--verbose", "--profile=d11"}} {
		name, rest, err := cutProfile(args)
		if err != nil || name != "d11" || len(rest) != 1 || rest[0] != "--verbose" {
			t.Errorf("cutProfile(%v) = %q %v %v", args, name, rest, err)
		}
	}
	if _, _, err := cutProfile([]string{"--profile"}); err == nil {
		t.Error("--profile without a name accepted")
	}
	if name, rest, _ := cutProfile([]string{"search", "ZCL_*"}); name != "" || len(rest) != 2 {
		t.Errorf("no profile: %q %v", name, rest)
	}
}

// The password is looked up per system and client first, then per system,
// then under vsp's own name-based variable.
func TestResolveProfile_PasswordOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		system string
		vars   map[string]string
		want   string
	}{
		"system and client": {"q11-200", map[string]string{"VSP_Q11_200_PASSWORD": "a", "VSP_Q11_PASSWORD": "b"}, "a"},
		"system only":       {"q11-200", map[string]string{"VSP_Q11_PASSWORD": "b", "VSP_Q11-200_PASSWORD": "c"}, "b"},
		"vsp's name-based":  {"q11-200", map[string]string{"VSP_Q11-200_PASSWORD": "c"}, "c"},
		"no sysid, by name": {"nosid", map[string]string{"VSP_NOSID_PASSWORD": "d"}, "d"},
		"another client's":  {"q11-200", map[string]string{"VSP_Q11_100_PASSWORD": "x"}, ""},
	} {
		e, _, err := resolveProfile(tc.system, []byte(profileJSON), env(tc.vars))
		switch {
		case tc.want == "" && (err == nil || !strings.Contains(err.Error(), "VSP_Q11_200_PASSWORD")):
			t.Errorf("%s: err = %v, want one naming the variables", name, err)
		case tc.want != "" && (err != nil || e["SAP_PASSWORD"] != tc.want):
			t.Errorf("%s: password %q, err %v; want %q", name, e["SAP_PASSWORD"], err, tc.want)
		}
	}
}

// The error names the variables, never a value.
func TestResolveProfile_FlagsAndLogon(t *testing.T) {
	e, flags, err := resolveProfile("d11", []byte(profileJSON), env(map[string]string{"VSP_D11_100_PASSWORD": "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"SAP_URL": "https://d11.example:44314", "SAP_USER": "dev", "SAP_CLIENT": "100",
		"SAP_SYSTEM": "d11", "SAP_LANGUAGE": "DE"} {
		if e[k] != v {
			t.Errorf("%s = %q, want %q", k, e[k], v)
		}
	}
	got := strings.Join(flags, " ")
	for _, want := range []string{"--mode hyperfocused", "--enable-transports", "--allowed-packages Z*,$TMP",
		"--allowed-transports D11K*,D22K*", "--cts-project D11_P00005", "--transport-target /PAR/"} {
		if !strings.Contains(got, want) {
			t.Errorf("flags %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "--read-only") || strings.Contains(got, "--disallowed-ops") {
		t.Errorf("flags %q carry settings d11 does not have", got)
	}
	_, flags, _ = resolveProfile("q11-200", []byte(profileJSON), env(map[string]string{"VSP_Q11_200_PASSWORD": "x"}))
	if got := strings.Join(flags, " "); !strings.Contains(got, "--transport-read-only") || !strings.Contains(got, "--disallowed-ops CDUAWLT") {
		t.Errorf("q11-200 flags %q", got)
	}
}

func TestResolveProfile_UnknownSystem(t *testing.T) {
	if _, _, err := resolveProfile("p99", []byte(profileJSON), env(nil)); err == nil || !strings.Contains(err.Error(), "p99") {
		t.Errorf("err = %v", err)
	}
}
