// signals: a light for every AI agent session (Linux and Windows; on macOS
// the Signals menu bar app and its own `signals` command do this).
//
//	signals install [agent...] [--no-tray]   wire up detected agents (or the ones named)
//	signals uninstall [agent...] [--purge]   remove hooks (everything if none named)
//	signals status                           agents, sessions and the tray
//	signals why                              why each session's light is the colour it is
//	signals menu                             the sessions as text
//	signals tray                             show the tray icon (Linux; Windows uses signals-tray)
//	signals hook <agent> [event]             what agents run (event JSON on stdin)
//	signals state                            every session, as JSON
//	signals --version
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/samhudson/signals/core/engine"
	"github.com/samhudson/signals/core/tray"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(1)
	}
	switch args[0] {
	case "hook":
		if len(args) < 2 {
			os.Exit(0)
		}
		event := ""
		if len(args) > 2 {
			event = args[2]
		}
		input, _ := io.ReadAll(os.Stdin)
		os.Exit(engine.Hook{Agent: args[1], Event: event, Input: input, Now: time.Now(),
			Stdout: os.Stdout, Stderr: os.Stderr, Gate: gate()}.Run())
	case "state":
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(engine.Sessions(time.Now()))
	case "install":
		install(args[1:])
	case "uninstall":
		uninstall(args[1:])
	case "status":
		os.Exit(status())
	case "why":
		why()
	case "menu":
		menu()
	case "tray":
		if err := tray.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage(0)
	default:
		usage(1)
	}
}

func usage(code int) {
	fmt.Print(`usage: signals install [agent...] [--no-tray]   wire up detected agents (or the ones named)
       signals uninstall [agent...] [--purge]   remove hooks (everything, including the tray, if none named)
       signals status                           agents, sessions and the tray
       signals why                              why each session's light is the colour it is
       signals menu                             the sessions as text
       signals tray                             show the tray icon
       signals --version
agents: ` + strings.Join(engine.Agents, " ") + "\n")
	os.Exit(code)
}

func info(f string, a ...any) { fmt.Printf("==> "+f+"\n", a...) }

func has(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// pick returns the agents named (rejecting unknown ones), or every detected agent.
func pick(args []string) []string {
	var named []string
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}
		ok := false
		for _, k := range engine.Agents {
			ok = ok || k == a
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown agent: %s (choose from: %s)\n", a, strings.Join(engine.Agents, " "))
			os.Exit(1)
		}
		named = append(named, a)
	}
	if len(named) > 0 {
		return named
	}
	var found []string
	for _, a := range engine.Agents {
		if engine.Detected(a) {
			found = append(found, a)
		}
	}
	return found
}

func testMode() bool { return os.Getenv("SIGNALS_NO_APP") != "" }

func install(args []string) {
	agents := pick(args)
	if len(agents) == 0 {
		fmt.Fprintln(os.Stderr, "No supported agents found. Name them, for example: signals install claude cursor")
		os.Exit(1)
	}
	exe, err := engine.Place()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Couldn't install Signals:", err)
		os.Exit(1)
	}
	os.MkdirAll(engine.SessionsDir(), 0o755)
	for _, a := range agents {
		if err := engine.Install(a, exe); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", engine.AgentName(a), err)
			continue
		}
		info("%s: hooks added to %s", engine.AgentName(a), tilde(engine.ConfigFile(a)))
	}
	if !testMode() {
		if changed, err := engine.SetUserPath(true); err != nil {
			fmt.Fprintln(os.Stderr, "Couldn't add Signals to your PATH:", err)
		} else if changed {
			info("Added %s to your PATH: open a new terminal to use the signals command", tilde(engine.InstallDir()))
		} else if !engine.OnPath() {
			info("Note: %s isn't on your PATH yet. Most systems add it when you next log in; otherwise add it to your shell profile.", tilde(engine.InstallDir()))
		}
	}
	if has(args, "--no-tray") || testMode() {
		info("Done. Restart running agent sessions so they pick up the hooks.")
		return
	}
	if err := engine.SetAutostart(true); err != nil {
		fmt.Fprintln(os.Stderr, "Couldn't set the tray to start at login:", err)
	}
	engine.StopTray()
	if err := engine.StartTray(); err != nil {
		fmt.Fprintln(os.Stderr, "Couldn't start the tray:", err)
	} else {
		info("Signals is in your tray, and starts at login")
	}
	info("Done. Restart running agent sessions so they pick up the hooks.")
}

func uninstall(args []string) {
	var named []string
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			named = append(named, a)
		}
	}
	agents := engine.Agents
	if len(named) > 0 {
		agents = pick(named)
	}
	for _, a := range agents {
		engine.Uninstall(a)
		info("%s: hooks removed", engine.AgentName(a))
	}
	if len(named) > 0 {
		return
	}
	if !testMode() {
		engine.SetAutostart(false)
		engine.StopTray()
		engine.SetUserPath(false)
	}
	os.RemoveAll(engine.SessionsDir())
	os.RemoveAll(filepath.Join(engine.SignalsHome(), "created"))
	if has(args, "--purge") {
		os.RemoveAll(engine.SignalsHome())
	}
	info("Uninstalled: hooks and tray removed. The program itself is in %s.", tilde(engine.InstallDir()))
}

func status() int {
	fmt.Println("Sessions")
	for _, a := range engine.Agents {
		switch {
		case engine.Installed(a):
			fmt.Printf("  🟢 %-18s hooks installed\n", engine.AgentName(a))
		case engine.Detected(a):
			fmt.Printf("  🟡 %-18s detected, not installed (signals install %s)\n", engine.AgentName(a), a)
		default:
			fmt.Printf("  ⚪ %-18s not found\n", engine.AgentName(a))
		}
	}
	s := engine.Sessions(time.Now())
	fmt.Printf("  %d sessions tracked%s\n", len(s.Rows), map[bool]string{true: ": " + tray.Summary(s), false: ""}[len(s.Rows) > 0])
	return 0
}

var dots = map[string]string{"working": "🔴", "waiting": "🟡", "done": "🟢", "error": "⚫"}

func why() {
	s := engine.Sessions(time.Now())
	if len(s.Rows) == 0 {
		fmt.Println("No agent sessions.")
		return
	}
	for _, r := range s.Rows {
		fmt.Printf("%s %s · %s · %s%s\n   %s\n", dots[r.Status], r.Name, r.Who, r.Label, detail(r), r.Why)
	}
}

func menu() {
	s := engine.Sessions(time.Now())
	if len(s.Rows) == 0 {
		fmt.Println("No agent sessions")
		return
	}
	var line strings.Builder
	for _, r := range s.Rows {
		line.WriteString(dots[r.Status])
	}
	fmt.Printf("%s\nSessions  %s\n", line.String(), tray.Summary(s))
	for _, r := range s.Rows {
		fmt.Printf("%s %s · %s · %s%s · %s\n", dots[r.Status], r.Name, r.Who, r.Label, detail(r), ago(s.Now-r.Updated))
	}
}

func detail(r engine.Row) string {
	if r.Detail != nil {
		return " (" + *r.Detail + ")"
	}
	return ""
}

func ago(s int64) string {
	switch {
	case s < 60:
		return "just now"
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	default:
		return fmt.Sprintf("%dh ago", s/3600)
	}
}

func tilde(p string) string {
	if h := engine.Home(); h != "" && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

// gate runs the Usage budget gate next to this program, when there is one.
func gate() func([]byte) (int, string) {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	path := filepath.Join(filepath.Dir(exe), "signals-gate")
	if fi, err := os.Stat(path); err != nil || fi.Mode()&0o111 == 0 {
		return nil
	}
	return func(input []byte) (int, string) {
		cmd := exec.Command(path)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if ee, ok := cmd.Run().(*exec.ExitError); ok {
			return ee.ExitCode(), stderr.String()
		}
		return 0, ""
	}
}
