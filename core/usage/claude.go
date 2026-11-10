package usage

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

type claudeUsage struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CC            *struct {
		E5  *int64 `json:"ephemeral_5m_input_tokens"`
		E1h int64  `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerToolUse *struct {
		WebSearch int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
	Speed string `json:"speed"`
}

type claudeLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	RequestID   string `json:"requestId"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID    string       `json:"id"`
		Model string       `json:"model"`
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
}

// CostState is Claude Code's own running total for one process of a session
// (a "cost-state" line): what verify compares the ledger against.
type CostState struct {
	SessionID  string                    `json:"sessionId"`
	StartTime  int64                     `json:"startTime"` // ms
	TotalCost  float64                   `json:"totalCostUSD"`
	ModelUsage map[string]CostStateModel `json:"modelUsage"`
	File       string                    `json:"-"`
	Line       int                       `json:"-"` // which line of the file it was on
}

type CostStateModel struct {
	InputTokens   int64   `json:"inputTokens"`
	OutputTokens  int64   `json:"outputTokens"`
	CacheRead     int64   `json:"cacheReadInputTokens"`
	CacheCreation int64   `json:"cacheCreationInputTokens"`
	WebSearch     int64   `json:"webSearchRequests"`
	CostUSD       float64 `json:"costUSD"`
}

var (
	markUsage     = []byte(`"usage"`)
	markAssistant = []byte(`"assistant"`)
	markCostState = []byte(`"cost-state"`)
)

func parseTime(ts string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	return t, err == nil
}

// readClaude reads a Claude Code transcript (or subagent transcript) from byte
// offset from: one call per API request, numbers only. Claude Code writes a
// line per content block of a reply, each with the same usage; they're
// merged by request id. Cost-state lines are returned when wanted (verify).
func readClaude(path string, from int64, loc *time.Location, wantCost bool) ([]Call, []CostState, int64, error) {
	sub := strings.Contains(filepath.ToSlash(path), "/subagents/")
	var calls []Call
	var states []CostState
	at := map[string]int{}
	n := 0
	off, err := readLines(path, from, func(line []byte) {
		n++
		if wantCost && bytes.Contains(line, markCostState) {
			var cs CostState
			var probe struct{ Type string }
			if json.Unmarshal(line, &probe) == nil && probe.Type == "cost-state" && json.Unmarshal(line, &cs) == nil {
				cs.File, cs.Line = path, n
				states = append(states, cs)
			}
			return
		}
		if !bytes.Contains(line, markUsage) || !bytes.Contains(line, markAssistant) {
			return
		}
		var l claudeLine
		if json.Unmarshal(line, &l) != nil || l.Type != "assistant" || l.Message.Usage == nil {
			return
		}
		m := l.Message.Model
		if m == "" || m == "<synthetic>" {
			return
		}
		id := l.RequestID
		if id == "" {
			id = l.Message.ID
		}
		if id == "" {
			return
		}
		t, ok := parseTime(l.Timestamp)
		if !ok {
			return
		}
		u := l.Message.Usage
		var w1h, w5 int64
		if u.CC != nil {
			w1h = u.CC.E1h
			if u.CC.E5 != nil {
				w5 = *u.CC.E5
			} else {
				w5 = u.CacheCreation - w1h
			}
		} else {
			w5 = u.CacheCreation
		}
		if w5 < 0 {
			w5 = 0
		}
		c := Call{V: 1, K: Key("claude", id), A: "claude", T: t.Unix(), D: t.In(loc).Format("2006-01-02"),
			S: l.SessionID, P: l.Cwd, M: m, In: u.InputTokens, CR: u.CacheRead, CW5: w5, CW1h: w1h,
			Out: u.OutputTokens, Fast: u.Speed == "fast", Sub: sub || l.IsSidechain}
		if u.ServerToolUse != nil {
			c.WS = u.ServerToolUse.WebSearch
		}
		if i, seen := at[c.K]; seen {
			calls[i] = mergeMax(calls[i], c)
			return
		}
		at[c.K] = len(calls)
		calls = append(calls, c)
	})
	return calls, states, off, err
}

// mergeMax combines two reads of the same call, keeping the larger of each
// count (a reply's lines normally repeat the same usage).
func mergeMax(a, b Call) Call {
	max := func(x, y int64) int64 {
		if y > x {
			return y
		}
		return x
	}
	a.In, a.CR, a.CW5, a.CW1h = max(a.In, b.In), max(a.CR, b.CR), max(a.CW5, b.CW5), max(a.CW1h, b.CW1h)
	a.Out, a.Rsn, a.WS = max(a.Out, b.Out), max(a.Rsn, b.Rsn), max(a.WS, b.WS)
	a.Fast = a.Fast || b.Fast
	return a
}
