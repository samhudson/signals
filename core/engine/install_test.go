package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func countOurs(t *testing.T, file string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	n := 0
	walkCommands(decode(data), func(c string) {
		if strings.Contains(c, "/signals") && strings.Contains(c, " hook ") {
			n++
		}
	})
	return n
}

func TestInstallKeepsEverythingElse(t *testing.T) {
	h := home(t)
	os.MkdirAll(filepath.Join(h, ".claude"), 0o755)
	settings := filepath.Join(h, ".claude", "settings.json")
	os.WriteFile(settings, []byte(`{
  "model": "opus",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "echo keep-me"}]}],
    "PostToolUse": [{"hooks": [{"type": "command", "command": "[ ! -x x ] || /old/libexec/trafficlight-hook claude PostToolUse"}]}]
  },
  "zeta": true
}`), 0o644)
	exe := filepath.Join(h, "bin", "signals")
	if err := Install("claude", exe); err != nil {
		t.Fatal(err)
	}
	if n := countOurs(t, settings); n != 8 {
		t.Errorf("claude: 8 events, got %d", n)
	}
	data, _ := os.ReadFile(settings)
	s := string(data)
	if !strings.Contains(s, "echo keep-me") || strings.Contains(s, "trafficlight-hook") {
		t.Errorf("other hooks kept, old ones replaced:\n%s", s)
	}
	if strings.Index(s, `"model"`) > strings.Index(s, `"hooks"`) || strings.Index(s, `"hooks"`) > strings.Index(s, `"zeta"`) {
		t.Errorf("key order kept:\n%s", s)
	}
	if _, err := os.Stat(settings + ".bak.signals"); err != nil {
		t.Errorf("a backup is saved")
	}
	Install("claude", exe)
	if n := countOurs(t, settings); n != 8 {
		t.Errorf("re-install is idempotent, got %d", n)
	}
	if !Installed("claude") {
		t.Errorf("shows as installed")
	}
	Uninstall("claude")
	data, _ = os.ReadFile(settings)
	if countOurs(t, settings) != 0 || !strings.Contains(string(data), "echo keep-me") || !strings.Contains(string(data), `"zeta"`) {
		t.Errorf("uninstall removes only ours:\n%s", data)
	}
}

func TestEachAgentsFormat(t *testing.T) {
	h := home(t)
	exe := filepath.Join(h, "bin", "signals")
	os.MkdirAll(filepath.Join(h, ".cursor"), 0o755)
	os.WriteFile(filepath.Join(h, ".cursor", "hooks.json"), []byte(`{"version":1,"hooks":{"stop":[{"command":"say done"}]}}`), 0o644)
	for _, a := range Agents {
		if err := Install(a, exe); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	want := map[string]int{"claude": 8, "codex": 7, "gemini": 6, "cursor": 5, "copilot": 5}
	for a, n := range want {
		if got := countOurs(t, ConfigFile(a)); got != n {
			t.Errorf("%s: %d events, got %d", a, n, got)
		}
	}
	data, _ := os.ReadFile(ConfigFile("gemini"))
	if !strings.Contains(string(data), `"timeout": 5000`) {
		t.Errorf("gemini timeouts are in milliseconds")
	}
	data, _ = os.ReadFile(ConfigFile("cursor"))
	if !strings.Contains(string(data), "say done") || !strings.Contains(string(data), `"version": 1`) {
		t.Errorf("cursor: other hooks and version kept:\n%s", data)
	}
	// Agents Signals created folders for are cleaned up again.
	Uninstall("gemini")
	if _, err := os.Stat(filepath.Join(h, ".gemini")); err == nil {
		t.Errorf("turning gemini off removes the folder Signals created")
	}
	Uninstall("cursor")
	if _, err := os.Stat(filepath.Join(h, ".cursor", "hooks.json")); err != nil {
		t.Errorf("a folder that was already there is kept")
	}
	Uninstall("copilot")
	if _, err := os.Stat(ConfigFile("copilot")); err == nil {
		t.Errorf("copilot's own file is removed")
	}
}

func TestBadConfigIsLeftAlone(t *testing.T) {
	h := home(t)
	os.MkdirAll(filepath.Join(h, ".claude"), 0o755)
	f := filepath.Join(h, ".claude", "settings.json")
	os.WriteFile(f, []byte(`{not json`), 0o644)
	if err := Install("claude", "/x/signals"); err == nil {
		t.Errorf("invalid JSON should be an error")
	}
	if data, _ := os.ReadFile(f); string(data) != `{not json` {
		t.Errorf("and the file is untouched")
	}
}
