package tempreport

import (
	"fmt"
	"strings"
)

// ValidateName accepts what a repository name can hold: letters, digits,
// underscore, namespace slashes and the $ of local packages. Anything else
// could break out of the literal it is written into.
func ValidateName(what, name string, max int) error {
	if name == "" {
		return fmt.Errorf("%s is required", what)
	}
	if len(name) > max {
		return fmt.Errorf("%s %s is longer than %d characters", what, name, max)
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '/' && r != '$' {
			return fmt.Errorf("%s %q contains %q", what, name, r)
		}
	}
	return nil
}

// Literal quotes s as an ABAP character literal.
func Literal(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Bool is b as an ABAP flag literal.
func Bool(b bool) string {
	if b {
		return "'X'"
	}
	return "' '"
}
