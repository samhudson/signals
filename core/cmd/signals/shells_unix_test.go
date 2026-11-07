//go:build !windows

package main

import "os/exec"

// hookShells: macOS and Linux agents run hook commands with sh.
func hookShells(command string) []*exec.Cmd {
	return []*exec.Cmd{exec.Command("sh", "-c", command)}
}
