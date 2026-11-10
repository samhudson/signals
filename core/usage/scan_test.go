package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// home makes a throwaway home folder with Claude Code and Codex logs in it.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("CODEX_HOME")
	return h
}

func login(t *testing.T, h, org, typ string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"organizationUuid": org, "organizationType": typ, "emailAddress": "dev@example.com"}})
	os.WriteFile(filepath.Join(h, ".claude.json"), b, 0o644)
}

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func ledger(t *testing.T, h string) *Ledger {
	return Open(filepath.Join(h, ".signals", "usage", "ledger"), time.UTC)
}

func scan(t *testing.T, l *Ledger) Stats {
	t.Helper()
	st, err := l.Scan(ScanOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func month(t *testing.T, l *Ledger) *Rollup {
	t.Helper()
	p, _ := LoadPricing("")
	return l.loadRollup("2026-11", p)
}

func total(r *Rollup, agent string) (calls int64, cost float64) {
	for _, row := range r.Rows {
		if agent == "" || row.A == agent {
			calls += row.Calls
			cost += row.Cost
		}
	}
	return
}

func TestScan(t *testing.T) {
	h := home(t)
	login(t, h, "org-1", "claude_team")
	cf := filepath.Join(h, ".claude", "projects", "-w-api", "s1.jsonl")
	write(t, cf, claudeA, claudeA2, claudeSynthetic, claudeUser)
	xf := filepath.Join(h, ".codex", "sessions", "2026", "11", "07", "rollout-a.jsonl")
	write(t, xf, codexMeta, codexTurn1, codexRec1, codexCount1)
	l := ledger(t, h)

	st := scan(t, l)
	if st.Calls != 2 || st.Read != 2 || !st.Complete {
		t.Fatalf("first scan: %+v", st)
	}
	r := month(t, l)
	if n, _ := total(r, "claude"); n != 1 {
		t.Errorf("claude calls: %d", n)
	}
	if n, _ := total(r, "codex"); n != 1 {
		t.Errorf("codex calls: %d", n)
	}
	_, cost := total(r, "claude")
	// 10 in x $4 + 500 out x $20 + 20000 cache reads x $0.20 + 2000 5m writes x $5 + 1000 1h writes x $8, per million
	if want := (10*4.0 + 500*20 + 20000*0.2 + 2000*5 + 1000*8) / 1e6; !near(cost, want) {
		t.Errorf("claude cost %v, want %v", cost, want)
	}

	// Nothing changed: nothing read, nothing counted twice.
	if st := scan(t, l); st.Read != 0 || st.Calls != 0 {
		t.Errorf("unchanged logs: %+v", st)
	}
	// A log grows: only the new call is counted.
	time.Sleep(10 * time.Millisecond)
	appendTo(t, cf, claudeFast)
	if st := scan(t, l); st.Calls != 1 {
		t.Errorf("grown log: %+v", st)
	}
	if n, _ := total(month(t, l), "claude"); n != 2 {
		t.Errorf("claude calls after growth: %d", n)
	}
	// The same calls rewritten into a new file (a copied session) aren't counted again.
	write(t, filepath.Join(h, ".claude", "projects", "-w-api", "copy.jsonl"), claudeA, claudeFast)
	if st := scan(t, l); st.Calls != 0 {
		t.Errorf("copied calls: %+v", st)
	}
	// A log rewritten from scratch is read again, without double counting.
	write(t, cf, claudeA, claudeFast)
	if st := scan(t, l); st.Calls != 0 {
		t.Errorf("rewritten log: %+v", st)
	}

	// Accounts: Claude from the login (hashed, with its kind); Codex from the session.
	calls := l.Calls("2026-11-01", "2026-11-30")
	for _, c := range calls {
		switch c.A {
		case "claude":
			if c.Acct != AccountID("anthropic", "org-1") || c.AK != "team" {
				t.Errorf("claude account: %+v", c)
			}
		case "codex":
			if c.Acct != AccountID("openai", "acct-raw-123") || c.AK != "team" || c.Plan != "business" {
				t.Errorf("codex account: %+v", c)
			}
		}
	}
	// Raw ids never reach the ledger.
	files, _ := filepath.Glob(filepath.Join(l.Dir, "*", "*"))
	files = append(files, filepath.Join(l.Dir, "checkpoints.json"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		for _, raw := range []string{"org-1", "acct-raw-123", "dev@example.com", "usage mean"} {
			if strings.Contains(string(data), raw) {
				t.Errorf("%s contains %q", filepath.Base(f), raw)
			}
		}
	}
}

func TestScanLoginSwitch(t *testing.T) {
	h := home(t)
	login(t, h, "org-1", "claude_team")
	cf := filepath.Join(h, ".claude", "projects", "-w", "s1.jsonl")
	write(t, cf, claudeA)
	l := ledger(t, h)
	scan(t, l)
	// Someone signs in to another account; calls since the last scan can't be told apart.
	login(t, h, "org-personal", "claude_max")
	appendTo(t, cf, claudeFast)
	if _, err := l.Scan(ScanOptions{Now: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, c := range l.Calls("2026-11-09", "2026-11-09") {
		if c.K == Key("claude", "req_2") && (c.Acct != "" || c.ASrc != "none") {
			t.Errorf("call after a login switch: %+v", c)
		}
	}
}

func TestScanBusyAndBudget(t *testing.T) {
	h := home(t)
	l := ledger(t, h)
	os.MkdirAll(l.Dir, 0o755)
	os.WriteFile(filepath.Join(l.Dir, "lock"), []byte(itoa(os.Getpid())+" "+itoa(int(now.Unix()))), 0o644)
	if st, err := l.Scan(ScanOptions{Now: now}); err != ErrBusy || !st.Busy {
		t.Errorf("a running scan blocks another: %+v %v", st, err)
	}
	// A stale lock (older than five minutes) is taken over.
	if _, err := l.Scan(ScanOptions{Now: now.Add(10 * time.Minute)}); err != nil {
		t.Errorf("stale lock: %v", err)
	}
	// A tiny budget stops early; the next scan carries on.
	write(t, filepath.Join(h, ".claude", "projects", "-w", "a.jsonl"), claudeA)
	write(t, filepath.Join(h, ".claude", "projects", "-w", "b.jsonl"), claudeFast)
	st, _ := l.Scan(ScanOptions{Now: now, Budget: time.Nanosecond})
	st2, _ := l.Scan(ScanOptions{Now: now})
	if st.Calls+st2.Calls != 2 {
		t.Errorf("budget resumes: %+v then %+v", st, st2)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestReprice(t *testing.T) {
	h := home(t)
	write(t, filepath.Join(h, ".claude", "projects", "-w", "s.jsonl"), claudeA)
	l := ledger(t, h)
	scan(t, l)
	// New prices: the kept days are priced again.
	f := filepath.Join(filepath.Dir(l.Dir), "pricing.json")
	os.WriteFile(f, []byte(`{"vendors":{"anthropic":{"cache_write_5m":1.25,"cache_write_1h":2}},"models":{"claude-opus-5-5":{"input":0,"output":1000000,"cache_read":0}}}`), 0o644)
	p, _ := LoadPricing(f)
	r := l.loadRollup("2026-11", p)
	if _, cost := total(r, "claude"); !near(cost, 500) {
		t.Errorf("repriced: %v", cost)
	}
}

func TestOldCallsOnlyOnBackfill(t *testing.T) {
	h := home(t)
	old := `{"type":"assistant","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"s0","requestId":"req_old","message":{"id":"m","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	write(t, filepath.Join(h, ".claude", "projects", "-w", "old.jsonl"), old)
	l := ledger(t, h)
	scan(t, l)
	p, _ := LoadPricing("")
	if n, _ := total(l.loadRollup("2026-08", p), "claude"); n != 1 {
		t.Errorf("first sight of an old log is a backfill: %d", n)
	}
	// The same old call turning up again in a new file (resumed session) isn't added.
	write(t, filepath.Join(h, ".claude", "projects", "-w", "resumed.jsonl"), old)
	scan(t, l)
	if n, _ := total(l.loadRollup("2026-08", p), "claude"); n != 1 {
		t.Errorf("old call from a resumed session: %d", n)
	}
}
