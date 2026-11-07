// signals-tray: the Signals tray icon on Windows, built as a background app
// (no console window) so it can start at login. Linux runs `signals tray`.
package main

import "github.com/samhudson/signals/core/tray"

func main() { tray.Run() }
