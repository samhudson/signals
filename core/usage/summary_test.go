package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	h := home(t)
	login(t, h, "org-1", "claude_pro")
	write(t, filepath.Join(h, ".claude", "projects", "-w-api", "s1.jsonl"), claudeA, claudeA2, claudeFast)
	unknown := strings.Replace(strings.Replace(claudeA, "req_1", "req_7", 1), "claude-opus-5-5", "claude-zeta-9", 1)
	write(t, filepath.Join(h, ".claude", "projects", "-w-api", "s2.jsonl"), unknown)
	write(t, filepath.Join(h, ".codex", "sessions", "2026", "11", "09", "rollout-a.jsonl"), codexMeta, codexTurn1,
		strings.Replace(codexRec1, "2026-11-07T01:56", "2026-11-09T11:56", 1), codexCount1)
	l := ledger(t, h)
	scan(t, l)
	p, _ := LoadPricing("")
	s := l.Summarize("2026-11", "model", now, p, true)
	tools := map[string]Tool{}
	for _, x := range s.Tools {
		tools[x.Agent] = x
	}
	if tools["claude"].Calls != 3 || tools["codex"].Calls != 1 || s.Total.Calls != 4 {
		t.Fatalf("calls by tool: %+v", s.Tools)
	}
	if u := tools["claude"].Unpriced; u.Calls != 1 || strings.Join(u.Models, ",") != "claude-zeta-9" {
		t.Errorf("an unknown model is counted, not priced: %+v", u)
	}
	if s.Label != "API-price estimate" || tools["codex"].Name != "Codex" || tools["claude"].Name != "Claude Code" {
		t.Errorf("labels: %+v", s)
	}
	if !near(s.Total.Cost, tools["claude"].Cost+tools["codex"].Cost) || tools["codex"].Cost <= 0 {
		t.Errorf("total: %+v", s.Total)
	}
	if len(s.Groups) != 3 {
		t.Errorf("by model: %+v", s.Groups)
	}
	// Today's sessions, with Codex next to Claude Code.
	agents := map[string]bool{}
	for _, x := range s.Sessions {
		agents[x.Agent] = true
	}
	if !agents["claude"] || !agents["codex"] {
		t.Errorf("today's sessions: %+v", s.Sessions)
	}
}

func TestVerify(t *testing.T) {
	h := home(t)
	start := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC).UnixMilli()
	cost := fmt.Sprintf(`{"type":"cost-state","sessionId":"s1","startTime":%d,"totalCostUSD":1,"modelUsage":{"claude-opus-5-5":{"inputTokens":15,"outputTokens":600,"cacheReadInputTokens":20000,"cacheCreationInputTokens":3000,"costUSD":1},"claude-haiku-5-5":{"inputTokens":9,"outputTokens":9,"costUSD":0.001}}}`+"\n", start)
	write(t, filepath.Join(h, ".claude", "projects", "-w", "s1.jsonl"), claudeA, claudeA2, claudeFast, cost)
	write(t, filepath.Join(h, ".codex", "sessions", "2026", "11", "07", "rollout-a.jsonl"), codexMeta, codexTurn1, codexRec1, codexCount1)
	p, _ := LoadPricing("")
	checks := Verify(p)
	if len(checks) != 2 {
		t.Fatalf("checks: %+v", checks)
	}
	for _, c := range checks {
		if !c.Match {
			t.Errorf("%s should match its own total: %+v", c.Agent, c)
		}
		if c.Agent == "claude" && !strings.Contains(c.Note, "claude-haiku-5-5") {
			t.Errorf("background calls only Claude Code saw are named: %q", c.Note)
		}
	}
}

func TestCLI(t *testing.T) {
	h := home(t)
	t.Setenv("SIGNALS_HOME", filepath.Join(h, ".signals"))
	write(t, filepath.Join(h, ".claude", "projects", "-w", "s1.jsonl"), claudeA)
	var out, errb bytes.Buffer
	if code := CLI([]string{"summary", "--json", "--month", "2026-11"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var s Summary
	if json.Unmarshal(out.Bytes(), &s) != nil || s.Total.Calls != 1 || s.Month != "2026-11" {
		t.Errorf("summary json: %s", out.String())
	}
	out.Reset()
	CLI([]string{"--month", "2026-11"}, &out, &errb)
	if !strings.Contains(out.String(), "Claude Code") || !strings.Contains(out.String(), "API-price estimate") {
		t.Errorf("text summary (the default): %s", out.String())
	}
	out.Reset()
	CLI([]string{"ledger", "--since", "2026-11-01"}, &out, &errb)
	if n := strings.Count(out.String(), "\n"); n != 1 || strings.Contains(out.String(), "dev@example.com") {
		t.Errorf("ledger lines: %q", out.String())
	}
	if code := CLI([]string{"bogus"}, &out, &errb); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code := CLI([]string{"summary", "--month"}, &out, &errb); code != 2 {
		t.Errorf("missing value: %d", code)
	}
}
