package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// home makes a throwaway home folder for one test (macOS, Linux and Windows).
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("SIGNALS_HOME", filepath.Join(h, ".signals"))
	return h
}

func run(t *testing.T, agent, event, payload string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := Hook{Agent: agent, Event: event, Input: []byte(payload), Now: time.Now(), Stdout: &out}.Run()
	return code, out.String()
}

func status(t *testing.T, agent, sid string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(SessionsDir(), agent+"-"+sid+".json"))
	if err != nil {
		return "none"
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	s, _ := m["status"].(string)
	return s
}

func TestEventsForEveryAgent(t *testing.T) {
	home(t)
	flows := []struct {
		agent, sid, payloadKey string
		steps                  [][2]string // event, expected state
	}{
		{"claude", "c1", "session_id", [][2]string{{"SessionStart", "done"}, {"UserPromptSubmit", "working"}, {"PermissionRequest", "waiting"}, {"PostToolUse", "working"}, {"Stop", "done"}}},
		{"codex", "x1", "session_id", [][2]string{{"UserPromptSubmit", "working"}, {"PermissionRequest", "waiting"}, {"Interrupt", "done"}}},
		{"gemini", "g1", "session_id", [][2]string{{"BeforeAgent", "working"}, {"Notification", "waiting"}, {"AfterTool", "working"}, {"AfterAgent", "done"}}},
		{"cursor", "u1", "conversation_id", [][2]string{{"beforeSubmitPrompt", "working"}, {"postToolUse", "working"}, {"stop", "done"}}},
		{"copilot", "p1", "sessionId", [][2]string{{"userPromptSubmitted", "working"}, {"agentStop", "done"}}},
	}
	for _, f := range flows {
		payload := `{"` + f.payloadKey + `":"` + f.sid + `","cwd":"/work/` + f.sid + `"}`
		for _, s := range f.steps {
			run(t, f.agent, s[0], payload)
			if got := status(t, f.agent, f.sid); got != s[1] {
				t.Errorf("%s %s: got %s, want %s", f.agent, s[0], got, s[1])
			}
		}
		run(t, f.agent, "SessionEnd", payload)
		run(t, f.agent, "sessionEnd", payload)
		if got := status(t, f.agent, f.sid); got != "none" {
			t.Errorf("%s: session end should remove the state, got %s", f.agent, got)
		}
	}
}

func TestClaudeNotificationsAndErrors(t *testing.T) {
	home(t)
	p := `{"session_id":"c2","cwd":"/w","notification_type":"%s"}`
	for _, typ := range []string{"permission_prompt", "elicitation_dialog", "agent_needs_input"} {
		run(t, "claude", "UserPromptSubmit", `{"session_id":"c2","cwd":"/w"}`)
		run(t, "claude", "Notification", strings.Replace(p, "%s", typ, 1))
		if got := status(t, "claude", "c2"); got != "waiting" {
			t.Errorf("Notification %s: got %s", typ, got)
		}
	}
	run(t, "claude", "UserPromptSubmit", `{"session_id":"c2","cwd":"/w"}`)
	run(t, "claude", "Notification", strings.Replace(p, "%s", "idle_prompt", 1))
	if got := status(t, "claude", "c2"); got != "working" {
		t.Errorf("idle_prompt should be ignored, got %s", got)
	}
	run(t, "claude", "StopFailure", `{"session_id":"c2","cwd":"/w","error_type":"rate_limit"}`)
	data, _ := os.ReadFile(filepath.Join(SessionsDir(), "claude-c2.json"))
	if !strings.Contains(string(data), `"detail":"rate_limit"`) || status(t, "claude", "c2") != "error" {
		t.Errorf("StopFailure should be an error with its reason: %s", data)
	}
	run(t, "cursor", "stop", `{"conversation_id":"u2","workspace_roots":["/w"],"status":"error"}`)
	if got := status(t, "cursor", "u2"); got != "error" {
		t.Errorf("cursor stop with error: got %s", got)
	}
}

func TestCursorAndCopilotSpecifics(t *testing.T) {
	home(t)
	if _, out := run(t, "cursor", "beforeSubmitPrompt", `{"conversation_id":"u3","workspace_roots":["/w/u3"]}`); strings.TrimSpace(out) != `{"continue": true}` {
		t.Errorf("cursor needs a decision on stdout, got %q", out)
	}
	run(t, "copilot", "userPromptSubmitted", `{"cwd":"/work/repo"}`)
	m, _ := filepath.Glob(filepath.Join(SessionsDir(), "copilot-dir-*.json"))
	if len(m) != 1 || len(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m[0]), "copilot-dir-"), ".json")) != 12 {
		t.Errorf("copilot without a session id: one session per folder, got %v", m)
	}
	if code, _ := run(t, "codex", "UserPromptSubmit", `{}`); code != 0 {
		t.Errorf("no session id: exit 0, got %d", code)
	}
}

func TestTitles(t *testing.T) {
	home(t)
	run(t, "codex", "UserPromptSubmit", `{"session_id":"t1","cwd":"/w","prompt":"\n  Fix the   login redirect bug\nand add a test"}`)
	run(t, "codex", "UserPromptSubmit", `{"session_id":"t1","cwd":"/w","prompt":"now something else"}`)
	run(t, "claude", "UserPromptSubmit", `{"session_id":"t2","cwd":"/w","prompt":"<ide_selection>lines 3 to 4:\nimport os\n</ide_selection>\n\nFix the flaky test"}`)
	run(t, "cursor", "beforeSubmitPrompt", `{"conversation_id":"t3","workspace_roots":["/w"],"prompt":"`+strings.Repeat("x", 80)+`"}`)
	title := func(f string) string {
		data, _ := os.ReadFile(filepath.Join(SessionsDir(), f))
		var m map[string]any
		json.Unmarshal(data, &m)
		s, _ := m["title"].(string)
		return s
	}
	if got := title("codex-t1.json"); got != "Fix the login redirect bug" {
		t.Errorf("first line, kept across prompts: %q", got)
	}
	if got := title("claude-t2.json"); got != "Fix the flaky test" {
		t.Errorf("editor context isn't the title: %q", got)
	}
	if got := []rune(title("cursor-t3.json")); len(got) != 48 {
		t.Errorf("titles clipped to 48 characters, got %d", len(got))
	}
}

func TestInterruptsAndNaming(t *testing.T) {
	h := home(t)
	os.MkdirAll(SessionsDir(), 0o755)
	now := time.Now()
	put := func(agent, sid, st, cwd string, age int) {
		data, _ := json.Marshal(map[string]any{"agent": agent, "session_id": sid, "status": st, "cwd": cwd, "updated": now.Unix() - int64(age)})
		os.WriteFile(filepath.Join(SessionsDir(), agent+"-"+sid+".json"), data, 0o644)
	}
	proj := filepath.Join(h, ".claude", "projects", "-code-alpha")
	os.MkdirAll(proj, 0o755)
	transcriptLine := func(typ, text string, ago int) string {
		e := map[string]any{"type": typ, "timestamp": now.Add(-time.Duration(ago) * time.Second).UTC().Format("2006-01-02T15:04:05.000Z"),
			"message": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}}
		b, _ := json.Marshal(e)
		return string(b) + "\n"
	}
	byID := func(s State, sid string) Row {
		for _, r := range s.Rows {
			if r.SessionID == sid {
				return r
			}
		}
		return Row{}
	}

	put("claude", "a1", "waiting", "/code/alpha/api", 60)
	os.WriteFile(filepath.Join(proj, "a1.jsonl"), []byte(transcriptLine("user", "[Request interrupted by user]", 10)), 0o644)
	if r := byID(Sessions(now), "a1"); r.Status != "done" || r.Detail == nil || *r.Detail != "interrupted" || !strings.Contains(r.Why, "interrupted at") {
		t.Errorf("Esc on a permission prompt should be done (interrupted): %+v", r)
	}
	put("claude", "a1", "working", "/code/alpha/api", 0)
	if r := byID(Sessions(now), "a1"); r.Status != "working" {
		t.Errorf("a new prompt after the interrupt should be working: %+v", r)
	}
	put("claude", "a1", "working", "/code/alpha/api", 60)
	os.WriteFile(filepath.Join(proj, "a1.jsonl"), []byte(
		transcriptLine("assistant", "[Request interrupted by user] appears in my code", 10)+
			transcriptLine("user", "why does it say [Request interrupted by user]?", 5)), 0o644)
	if r := byID(Sessions(now), "a1"); r.Status != "working" {
		t.Errorf("the words inside a message are not an interrupt: %+v", r)
	}
	os.WriteFile(filepath.Join(proj, "a1.jsonl"), []byte(`{"type":"ai-title","aiTitle":"Refactor billing service"}`+"\n"), 0o644)
	if r := byID(Sessions(now), "a1"); r.Name != "Refactor billing service" {
		t.Errorf("Claude's own title: %q", r.Name)
	}

	put("cursor", "k1", "working", "/code/beta/api", 0)
	put("codex", "d1", "done", "/code/gamma", 30)
	put("gemini", "old", "working", "/code/stale", 50000)
	s := Sessions(now)
	if r := byID(s, "k1"); r.Name != "beta/api" {
		t.Errorf("two folders named api: parent shown, got %q", r.Name)
	}
	if _, err := os.Stat(filepath.Join(SessionsDir(), "gemini-old.json")); err == nil {
		t.Errorf("sessions silent for 12 hours are pruned")
	}
	put("copilot", "w1", "working", `C:\Users\dev\code\web`, 0)
	if r := byID(Sessions(now), "w1"); r.Name != "web" {
		t.Errorf("Windows paths name the folder too, got %q", r.Name)
	}
	if s.Rows[len(s.Rows)-1].Status != "done" || s.Counts["working"] != 2 {
		t.Errorf("sorted needs-you, working, done; counts: %+v", s.Counts)
	}
}
