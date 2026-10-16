//go:build !windows

package main

// exitWithParent does nothing outside Windows: there a killed parent closes
// the child's stdin, and vspx ends on the end of its input.
func exitWithParent() {}
