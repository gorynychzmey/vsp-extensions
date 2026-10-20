//go:build !windows

package main

import "os"

func lookupEnv(name string) string { return os.Getenv(name) }
