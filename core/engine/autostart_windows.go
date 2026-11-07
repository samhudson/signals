//go:build windows

package engine

import (
	"os/exec"
	"syscall"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutostart starts the tray at login (the per-user Run key; no admin needed).
func SetAutostart(on bool) error {
	if !on {
		exec.Command("reg", "delete", runKey, "/v", "Signals", "/f").Run()
		return nil
	}
	prog, _ := TrayProgram()
	return exec.Command("reg", "add", runKey, "/v", "Signals", "/t", "REG_SZ", "/d", `"`+prog+`"`, "/f").Run()
}

// StartTray launches the tray now, without a console window.
func StartTray() error {
	prog, args := TrayProgram()
	cmd := exec.Command(prog, args...)
	const detached, newGroup = 0x00000008, 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detached | newGroup, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
