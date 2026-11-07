//go:build darwin

package tray

import "errors"

// Run isn't used on macOS: the Signals menu bar app does this there.
func Run() error { return errors.New("on macOS, use the Signals menu bar app") }
