//go:build !darwin

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/systray"

	"github.com/samhudson/signals/core/engine"
)

const slots = 12

var dots = map[string]string{"working": "🔴", "waiting": "🟡", "done": "🟢", "error": "⚫"}

// Run shows the tray icon until Quit. Only one copy runs at a time.
func Run() error {
	pidFile := filepath.Join(engine.SignalsHome(), "tray.pid")
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid != os.Getpid() && engine.Alive(pid) {
			return nil
		}
	}
	os.MkdirAll(engine.SignalsHome(), 0o755)
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile)
	systray.Run(ready, func() {})
	return nil
}

func ready() {
	systray.SetTitle("")
	systray.SetTooltip("Signals")
	head := systray.AddMenuItem("Signals", "")
	head.Disable()
	systray.AddSeparator()
	items := make([]*systray.MenuItem, slots)
	folders := make([]string, slots)
	for i := range items {
		items[i] = systray.AddMenuItem("", "")
		items[i].Hide()
		go func(i int) {
			for range items[i].ClickedCh {
				openFolder(folders[i])
			}
		}(i)
	}
	empty := systray.AddMenuItem("No agent sessions", "")
	empty.Disable()
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Signals", "")
	go func() { <-quit.ClickedCh; systray.Quit() }()

	last := "-"
	update := func() {
		s := engine.Sessions(time.Now())
		urgent := Urgent(s)
		if urgent != last {
			icon := IconPNG(urgent, 32)
			if runtime.GOOS == "windows" {
				icon = ICO(icon, 32)
			}
			systray.SetIcon(icon)
			last = urgent
		}
		sum := Summary(s)
		systray.SetTooltip("Signals" + map[bool]string{true: ": " + sum, false: ""}[sum != ""])
		head.SetTitle("Signals" + map[bool]string{true: "   " + sum, false: ""}[sum != ""])
		for i := range items {
			if i < len(s.Rows) {
				r := s.Rows[i]
				label := fmt.Sprintf("%s %s · %s · %s", dots[r.Status], r.Name, r.Who, r.Label)
				if r.Detail != nil {
					label += " (" + *r.Detail + ")"
				}
				if i == slots-1 && len(s.Rows) > slots {
					label = fmt.Sprintf("+%d more (signals status)", len(s.Rows)-slots+1)
				}
				items[i].SetTitle(label)
				items[i].SetTooltip(r.Cwd)
				folders[i] = r.Cwd
				items[i].Show()
			} else {
				items[i].Hide()
			}
		}
		if len(s.Rows) == 0 {
			empty.Show()
		} else {
			empty.Hide()
		}
	}
	update()
	go func() {
		for range time.Tick(2 * time.Second) {
			update()
		}
	}()
}

func openFolder(p string) {
	if p == "" {
		return
	}
	if runtime.GOOS == "windows" {
		exec.Command("explorer", filepath.FromSlash(p)).Start()
		return
	}
	exec.Command("xdg-open", p).Start()
}
