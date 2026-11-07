//go:build !linux && !windows

package engine

// On macOS the Signals menu bar app has its own login item.
func SetAutostart(on bool) error { return nil }
func StartTray() error           { return nil }
