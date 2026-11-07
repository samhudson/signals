//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// hookShells: on Windows, Claude Code runs hooks with Git Bash; other agents
// may use the Command Prompt. The command is passed exactly as written (Go
// would otherwise add its own quoting for cmd.exe).
func hookShells(command string) []*exec.Cmd {
	var shells []*exec.Cmd
	for _, bash := range []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Program Files\Git\usr\bin\bash.exe`} {
		if _, err := os.Stat(bash); err == nil {
			shells = append(shells, exec.Command(bash, "-c", command))
			break
		}
	}
	cmd := exec.Command(os.Getenv("ComSpec"))
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /c "` + command + `"`}
	return append(shells, cmd)
}
