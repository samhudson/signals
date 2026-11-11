package usage

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Check compares the ledger's reading of one log with the agent's own total.
type Check struct {
	Agent  string  `json:"agent"`
	Log    string  `json:"log"` // the session id (file name), not the path
	Match  bool    `json:"match"`
	Ours   int64   `json:"ours_tokens"`
	Theirs int64   `json:"their_tokens"`
	Cost   float64 `json:"ours_cost,omitempty"`
	Their  float64 `json:"their_cost,omitempty"`
	Note   string  `json:"note,omitempty"`
}

// Verify compares what the readers count with each agent's own totals:
// Claude Code's cost-state (per process of a session) and Codex's running
// thread totals. Read-only: the ledger isn't touched.
func Verify(p *Pricing) []Check {
	var out []Check
	filepath.WalkDir(claudeProjects(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		calls, states, _, _ := readClaude(path, 0, time.UTC, true)
		if len(states) == 0 {
			return nil
		}
		// The last cost-state of each process is its total so far.
		last := map[int64]CostState{}
		for _, s := range states {
			last[s.StartTime] = s
		}
		for _, s := range last {
			out = append(out, checkClaude(path, s, calls, p))
		}
		return nil
	})
	filepath.WalkDir(codexSessions(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") {
			return nil
		}
		if c, ok := checkCodex(path); ok {
			out = append(out, c)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Agent+out[i].Log < out[j].Agent+out[j].Log })
	return out
}

func checkClaude(path string, s CostState, calls []Call, p *Pricing) Check {
	c := Check{Agent: "claude", Log: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	// The process's calls: from its start time, up to where its total was written.
	ours := map[string]*Row{}
	for _, call := range calls {
		if call.T*1000 < s.StartTime-1000 || call.line > s.Line {
			continue
		}
		r := ours[call.M]
		if r == nil {
			r = &Row{}
			ours[call.M] = r
		}
		r.add(call, p)
	}
	for _, r := range ours {
		c.Cost += r.Cost
		c.Ours += r.Tokens()
	}
	var extra []string
	c.Match = true
	for m, u := range s.ModelUsage {
		c.Theirs += u.InputTokens + u.CacheRead + u.CacheCreation + u.OutputTokens
		c.Their += u.CostUSD
		r := ours[m]
		if r == nil {
			extra = append(extra, m)
			continue
		}
		if r.In != u.InputTokens || r.Out != u.OutputTokens || r.CR != u.CacheRead || r.CW5+r.CW1h != u.CacheCreation {
			c.Match = false
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		c.Note = "only in Claude Code's own total (calls not written to the transcript): " + strings.Join(extra, ", ")
	}
	switch {
	case c.Match && len(extra) == 0 && math.Abs(c.Cost-c.Their) > 0.0005*math.Max(1, c.Their):
		c.Note = "tokens match; cost differs (prices)"
	case !c.Match && c.Ours > c.Theirs:
		c.Note = "the transcript has calls Claude Code's own total doesn't count (seen in older sessions); the ledger follows the transcript"
	}
	return c
}

// checkCodex compares the sum of a Codex log's calls with the final running
// total of each thread in it.
func checkCodex(path string) (Check, bool) {
	var x codexContext
	calls, _, _ := readCodex(path, 0, time.UTC, &x)
	totals := map[string]codexTokens{}
	readLines(path, 0, func(line []byte) {
		if !bytes.Contains(line, []byte(`"token_usage_record"`)) {
			return
		}
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				ThreadID string       `json:"thread_id"`
				Thread   *codexTokens `json:"thread_token_usage"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "token_usage_record" && l.Payload.Thread != nil {
			totals[l.Payload.ThreadID] = *l.Payload.Thread
		}
	})
	if len(totals) == 0 {
		return Check{}, false
	}
	c := Check{Agent: "codex", Log: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	var theirIn, theirOut, ourIn, ourOut int64
	for _, t := range totals {
		theirIn += t.Input
		theirOut += t.Output
	}
	for _, call := range calls {
		ourIn += call.In + call.CR + call.CW5
		ourOut += call.Out
	}
	c.Ours, c.Theirs = ourIn+ourOut, theirIn+theirOut
	c.Match = ourIn == theirIn && ourOut == theirOut
	return c, true
}
