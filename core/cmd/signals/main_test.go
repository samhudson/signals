package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// End to end on this platform: build signals, install it into a throwaway
// home, then run the exact hook commands it wrote, the way agents run them.
func TestInstallAndHooksEndToEnd(t *testing.T) {
	h := t.TempDir()
	exe := filepath.Join(t.TempDir(), "signals")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := append(os.Environ(), "HOME="+h, "USERPROFILE="+h, "SIGNALS_HOME="+filepath.Join(h, ".signals"),
		"SIGNALS_INSTALL_DIR="+filepath.Join(h, "programs"), "SIGNALS_NO_APP=1")
	run := func(args ...string) string {
		cmd := exec.Command(exe, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("signals %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("install", "claude", "cursor")
	installed := filepath.Join(h, "programs", filepath.Base(exe))
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("install copies the program to a stable place: %v", err)
	}

	// The command Claude Code would run for UserPromptSubmit, run through the shell agents use.
	data, _ := os.ReadFile(filepath.Join(h, ".claude", "settings.json"))
	var settings map[string]map[string][]map[string][]map[string]any
	json.Unmarshal(data, &settings)
	command, _ := settings["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"].(string)
	if !strings.Contains(command, filepath.ToSlash(installed)) && !strings.Contains(command, installed) {
		t.Fatalf("hooks point at the installed program: %s", command)
	}
	hook := func(command, payload string) {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", command)
		} else {
			cmd = exec.Command("sh", "-c", command)
		}
		cmd.Env = env
		cmd.Stdin = strings.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook %q: %v\n%s", command, err, out)
		}
	}
	hook(command, `{"session_id":"e2e","cwd":"/work/api","prompt":"Ship the release"}`)
	if out := run("menu"); !strings.Contains(out, "🔴 Ship the release · Claude Code · working") {
		t.Errorf("the session shows as working:\n%s", out)
	}
	if out := run("why"); !strings.Contains(out, "last hook event: working") {
		t.Errorf("signals why explains it:\n%s", out)
	}
	if out := run("status"); !strings.Contains(out, "Claude Code") || !strings.Contains(out, "hooks installed") {
		t.Errorf("status shows the wired agents:\n%s", out)
	}

	run("uninstall")
	data, _ = os.ReadFile(filepath.Join(h, ".claude", "settings.json"))
	if strings.Contains(string(data), " hook ") {
		t.Errorf("uninstall removes the hooks:\n%s", data)
	}
}
