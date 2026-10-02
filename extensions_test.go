package extensions_test

import (
	"testing"

	"github.com/gorynychzmey/vsp-extensions/forms"
	"github.com/gorynychzmey/vsp-extensions/idoc"
	"github.com/gorynychzmey/vsp-extensions/sxci"
	"github.com/gorynychzmey/vsp-extensions/trimport"
	"github.com/oisee/vibing-steampunk/pkg/mcpext/mcpexttest"
)

// The extensions together, as vspx runs them: valid, no collisions, refused
// under --read-only where they write, and covered by the help.
func TestExtensionsAreWellBehaved(t *testing.T) {
	mcpexttest.Check(t, forms.New(), sxci.New(), idoc.New(), trimport.New())
}
