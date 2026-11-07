//go:build linux

package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func autostartFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(Home(), ".config")
	}
	return filepath.Join(dir, "autostart", "signals.desktop")
}

// SetAutostart starts the tray at login (an XDG autostart entry).
func SetAutostart(on bool) error {
	f := autostartFile()
	if !on {
		os.Remove(f)
		return nil
	}
	prog, args := TrayProgram()
	os.MkdirAll(filepath.Dir(f), 0o755)
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Signals\nComment=A light for every AI agent session\nExec=\"%s\" %s\nX-GNOME-Autostart-enabled=true\nTerminal=false\n", prog, strings.Join(args, " "))
	return os.WriteFile(f, []byte(entry), 0o644)
}

// StartTray launches the tray now, detached from this terminal. With no
// desktop session (a server, or over SSH) there's nowhere to show it.
func StartTray() error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return ErrNoDesktop
	}
	prog, args := TrayProgram()
	cmd := exec.Command(prog, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
