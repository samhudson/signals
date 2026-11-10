package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestReadLines(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.jsonl")
	write(t, f, "a\r\n", "b\n", "c") // c is still being written
	var got []string
	off, err := readLines(f, 0, func(l []byte) { got = append(got, string(l)) })
	if err != nil || strings.Join(got, ",") != "a,b" || off != int64(len("a\r\nb\n")) {
		t.Fatalf("got %v off %d err %v", got, off, err)
	}
	appendTo(t, f, "\n")
	got = nil
	off2, _ := readLines(f, off, func(l []byte) { got = append(got, string(l)) })
	if strings.Join(got, ",") != "c" || off2 != off+2 {
		t.Errorf("the finished line is read next time: %v %d", got, off2)
	}
	// An over-long line is skipped, not fatal.
	old := maxLine
	maxLine = 8
	defer func() { maxLine = old }()
	write(t, f, "short\n", strings.Repeat("x", 3_000_000)+"\n", "after\n")
	got = nil
	readLines(f, 0, func(l []byte) { got = append(got, string(l)) })
	if strings.Join(got, ",") != "short,after" {
		t.Errorf("over-long line: %v", got)
	}
}

const claudeA = `{"type":"assistant","timestamp":"2026-11-09T10:00:00.000Z","sessionId":"s1","cwd":"/w/api","requestId":"req_1","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"thinking"}],"usage":{"input_tokens":10,"output_tokens":500,"cache_creation_input_tokens":3000,"cache_read_input_tokens":20000,"cache_creation":{"ephemeral_1h_input_tokens":1000}}}}` + "\n"
const claudeA2 = `{"type":"assistant","timestamp":"2026-11-09T10:00:01.000Z","sessionId":"s1","cwd":"/w/api","requestId":"req_1","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"text","text":"usage"}],"usage":{"input_tokens":10,"output_tokens":500,"cache_creation_input_tokens":3000,"cache_read_input_tokens":20000,"cache_creation":{"ephemeral_1h_input_tokens":1000}}}}` + "\n"
const claudeFast = `{"type":"assistant","timestamp":"2026-11-09T23:30:00.000Z","sessionId":"s1","cwd":"/w/api","requestId":"req_2","message":{"id":"msg_2","model":"claude-opus-5-5","usage":{"input_tokens":5,"output_tokens":100,"cache_read_input_tokens":0,"speed":"fast","server_tool_use":{"web_search_requests":2}}}}` + "\n"
const claudeSynthetic = `{"type":"assistant","timestamp":"2026-11-09T10:01:00.000Z","sessionId":"s1","requestId":"req_3","message":{"id":"msg_3","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}` + "\n"
const claudeUser = `{"type":"user","timestamp":"2026-11-09T10:02:00.000Z","sessionId":"s1","message":{"role":"user","content":"what does \"usage\" mean for the assistant"}}` + "\n"
const claudeCost = `{"type":"cost-state","sessionId":"s1","startTime":1794218400000,"totalCostUSD":1.5,"modelUsage":{"claude-opus-5-5":{"inputTokens":15,"outputTokens":600,"cacheReadInputTokens":20000,"cacheCreationInputTokens":3000,"webSearchRequests":2,"costUSD":1.5}}}` + "\n"

func TestReadClaude(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "-w-api", "s1.jsonl")
	write(t, f, claudeA, claudeA2, claudeSynthetic, claudeUser, claudeFast, claudeCost, claudeA[:40]) // last line unfinished
	calls, states, off, err := readClaude(f, 0, time.UTC, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("one call per request (a reply's lines merged, synthetic skipped): got %d %+v", len(calls), calls)
	}
	a := calls[0]
	if a.In != 10 || a.Out != 500 || a.CR != 20000 || a.CW1h != 1000 || a.CW5 != 2000 || a.M != "claude-opus-5-5" || a.D != "2026-11-09" || a.S != "s1" || a.Sub {
		t.Errorf("first call: %+v", a)
	}
	if a.K != Key("claude", "req_1") {
		t.Error("keyed by request id")
	}
	if b := calls[1]; !b.Fast || b.WS != 2 {
		t.Errorf("fast and web searches: %+v", b)
	}
	if len(states) != 1 || states[0].ModelUsage["claude-opus-5-5"].OutputTokens != 600 {
		t.Errorf("cost-state: %+v", states)
	}
	data, _ := os.ReadFile(f)
	if off != int64(len(data)-40) {
		t.Errorf("stops before the unfinished line: %d of %d", off, len(data))
	}
	// Days are local days.
	ny, _ := time.LoadLocation("America/New_York")
	calls, _, _, _ = readClaude(f, 0, ny, false)
	if calls[1].D != "2026-11-09" || calls[0].D != "2026-11-09" {
		t.Errorf("local days: %s %s", calls[0].D, calls[1].D)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	calls, _, _, _ = readClaude(f, 0, tokyo, false)
	if calls[1].D != "2026-11-10" {
		t.Errorf("23:30 UTC is the next day in Tokyo: %s", calls[1].D)
	}
	// Subagent transcripts are marked.
	sf := filepath.Join(dir, "-w-api", "s1", "subagents", "agent-a1.jsonl")
	write(t, sf, strings.Replace(claudeFast, "req_2", "req_9", 1))
	calls, _, _, _ = readClaude(sf, 0, time.UTC, false)
	if len(calls) != 1 || !calls[0].Sub {
		t.Errorf("subagent: %+v", calls)
	}
}

const codexMeta = `{"timestamp":"2026-11-07T01:56:30.000Z","type":"session_meta","payload":{"id":"sess-1","session_id":"sess-1","cwd":"/w/web","originator":"codex_vscode","cli_version":"0.167.0","model_provider":"openai","creator_account_id":"acct-raw-123","base_instructions":{"text":"..."}}}` + "\n"
const codexTurn1 = `{"timestamp":"2026-11-07T01:56:31.000Z","type":"turn_context","payload":{"turn_id":"t1","model":"gpt-6.1-sol","cwd":"/w/web"}}` + "\n"
const codexRec1 = `{"timestamp":"2026-11-07T01:56:36.987Z","type":"token_usage_record","payload":{"thread_id":"th","turn_id":"t1","session_id":"sess-1","response_id":"resp_1","usage":{"input_tokens":21112,"cached_input_tokens":13440,"cache_write_input_tokens":100,"output_tokens":93,"reasoning_output_tokens":40,"total_tokens":21205},"thread_token_usage":{"input_tokens":21112,"cached_input_tokens":13440,"cache_write_input_tokens":100,"output_tokens":93,"reasoning_output_tokens":40,"total_tokens":21205}}}` + "\n"
const codexCount1 = `{"timestamp":"2026-11-07T01:56:37.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":21112,"cached_input_tokens":13440,"output_tokens":93,"reasoning_output_tokens":40,"total_tokens":21205},"total_token_usage":{"input_tokens":21112,"cached_input_tokens":13440,"output_tokens":93,"reasoning_output_tokens":40,"total_tokens":21205}},"rate_limits":{"plan_type":"business","primary":{"used_percent":3}}}}` + "\n"
const codexTurn2 = `{"timestamp":"2026-11-07T02:10:00.000Z","type":"turn_context","payload":{"turn_id":"t2","model":"gpt-6-astra","cwd":"/w/web"}}` + "\n"
const codexRec2 = `{"timestamp":"2026-11-07T02:10:05.000Z","type":"token_usage_record","payload":{"thread_id":"th","turn_id":"t2","session_id":"sess-1","response_id":"resp_2","usage":{"input_tokens":5000,"cached_input_tokens":4000,"cache_write_input_tokens":0,"output_tokens":700,"reasoning_output_tokens":0,"total_tokens":5700}}}` + "\n"
const codexCount2 = `{"timestamp":"2026-11-07T02:10:06.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":5000,"cached_input_tokens":4000,"output_tokens":700,"total_tokens":5700},"total_token_usage":{"input_tokens":26112,"output_tokens":793,"total_tokens":26905}},"rate_limits":{"plan_type":"business"}}}` + "\n"

func TestReadCodex(t *testing.T) {
	f := filepath.Join(t.TempDir(), "2026", "11", "07", "rollout-a.jsonl")
	write(t, f, codexMeta, codexTurn1, codexRec1, codexCount1)
	var x codexContext
	calls, off, err := readCodex(f, 0, time.UTC, &x)
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls %+v err %v", calls, err)
	}
	c := calls[0]
	// input includes cached and cache writes: 21112 - 13440 - 100
	if c.In != 7572 || c.CR != 13440 || c.CW5 != 100 || c.Out != 93 || c.Rsn != 40 || c.M != "gpt-6.1-sol" {
		t.Errorf("tokens/model: %+v", c)
	}
	if c.Plan != "business" || c.AK != "team" || c.Via != "codex_vscode" || c.S != "sess-1" || c.P != "/w/web" {
		t.Errorf("plan arrives after the call; app, session, folder: %+v", c)
	}
	if c.Acct == "" || c.Acct == "acct-raw-123" || c.Acct != AccountID("openai", "acct-raw-123") || c.ASrc != "session" {
		t.Errorf("account is hashed, never stored raw: %+v", c)
	}
	// Later, the file grows: only the new part is read, with what was learned kept.
	appendTo(t, f, codexTurn2+codexRec2+codexCount2)
	calls, _, _ = readCodex(f, off, time.UTC, &x)
	if len(calls) != 1 || calls[0].M != "gpt-6-astra" || calls[0].In != 1000 || calls[0].Plan != "business" || calls[0].Acct != c.Acct {
		t.Errorf("second pass: %+v", calls)
	}
}

func TestReadCodexOlderLogs(t *testing.T) {
	f := filepath.Join(t.TempDir(), "rollout-old.jsonl")
	// No per-call records: running totals only, with a repeated total to skip.
	write(t, f, codexMeta, codexTurn1, codexCount1, codexCount1, codexCount2)
	var x codexContext
	calls, _, _ := readCodex(f, 0, time.UTC, &x)
	if len(calls) != 2 {
		t.Fatalf("one call per change of the running total: %+v", calls)
	}
	if calls[0].In != 21112-13440 || calls[1].Out != 700 {
		t.Errorf("from last_token_usage: %+v", calls)
	}
	// A log with per-call records never also counts its totals.
	f2 := filepath.Join(t.TempDir(), "rollout-new.jsonl")
	write(t, f2, codexMeta, codexTurn1, codexCount1, codexRec1)
	var y codexContext
	calls, _, _ = readCodex(f2, 0, time.UTC, &y)
	if len(calls) != 1 {
		t.Errorf("records win over totals: %+v", calls)
	}
}

func TestKinds(t *testing.T) {
	for in, want := range map[string]string{"claude_pro": "individual", "claude_max": "individual", "claude_team": "team", "claude_enterprise": "enterprise", "": "unknown"} {
		if got := claudeKind(in); got != want {
			t.Errorf("claudeKind(%q) = %s", in, got)
		}
	}
	for in, want := range map[string]string{"plus": "individual", "pro": "individual", "business": "team", "team": "team", "enterprise": "enterprise", "edu": "enterprise", "": "unknown"} {
		if got := codexKind(in); got != want {
			t.Errorf("codexKind(%q) = %s", in, got)
		}
	}
}
