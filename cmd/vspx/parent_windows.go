package main

import (
	"os"
)

// exitWithParent ends this process when the process that started it ends.
//
// An MCP client starts vspx through a launcher script (powershell running
// vsp-mcp.ps1). When the client reconnects it kills the launcher, but on
// Windows the child keeps running, and its stdin is not closed either, so it
// never sees the end of input it would otherwise exit on. Every reconnect
// left one process per server behind, each holding memory and SAP sessions,
// until new servers no longer started within the client's timeout.
//
// VSPX_NO_PARENT_WATCH=1 turns it off.
func exitWithParent() {
	if os.Getenv("VSPX_NO_PARENT_WATCH") == "1" {
		return
	}
	parent, err := os.FindProcess(os.Getppid())
	if err != nil {
		return // the parent is gone already, or cannot be watched
	}
	go func() {
		if _, err := parent.Wait(); err == nil {
			os.Exit(0)
		}
	}()
}
