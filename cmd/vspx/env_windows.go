package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// lookupEnv reads a variable from the process environment, then from the
// user's stored environment (HKCU\Environment): a password set with
// [Environment]::SetEnvironmentVariable(..., 'User') after the MCP client
// started is not in the environment the client hands its servers.
func lookupEnv(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}
