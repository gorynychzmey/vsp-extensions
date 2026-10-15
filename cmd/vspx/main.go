// Command vspx is vsp with the extensions of this repository built in. It
// takes every flag, command and setting vsp does.
package main

import (
	"github.com/gorynychzmey/vsp-extensions/ctsplus"
	"github.com/gorynychzmey/vsp-extensions/customizing"
	"github.com/gorynychzmey/vsp-extensions/forms"
	"github.com/gorynychzmey/vsp-extensions/idoc"
	"github.com/gorynychzmey/vsp-extensions/sxci"
	"github.com/gorynychzmey/vsp-extensions/trimport"
	"github.com/oisee/vibing-steampunk/pkg/vsp"
)

// Set at build time, as for vsp.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	vsp.Version, vsp.Commit, vsp.BuildDate = Version, Commit, BuildDate
	vsp.Run(forms.New(), sxci.New(), idoc.New(), trimport.New(), ctsplus.New(), customizing.New())
}
