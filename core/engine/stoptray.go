package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StopTray quits a running tray (it records its process id).
func StopTray() {
	f := filepath.Join(SignalsHome(), "tray.pid")
	data, err := os.ReadFile(f)
	if err != nil {
		return
	}
	if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 && Alive(pid) {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	}
	os.Remove(f)
}
