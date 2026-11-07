// Package engine is Signals' cross-platform core: the hook each agent runs on
// its events, and the session state the menu bar, tray and phone read. It
// matches libexec/signals-hook and libexec/signals-state, which it will replace.
package engine

import (
	"os"
	"path/filepath"
)

// Home is the user's home folder: $HOME on macOS and Linux, %USERPROFILE% on Windows.
func Home() string {
	h, _ := os.UserHomeDir()
	return h
}

// SignalsHome is where Signals keeps its state (~/.signals, or $SIGNALS_HOME).
func SignalsHome() string {
	if v := os.Getenv("SIGNALS_HOME"); v != "" {
		return v
	}
	return filepath.Join(Home(), ".signals")
}

func SessionsDir() string { return filepath.Join(SignalsHome(), "sessions") }
func ClaudeDir() string   { return filepath.Join(Home(), ".claude") }

// Alive reports whether a process is still running.
func Alive(pid int) bool { return alive(pid) }
