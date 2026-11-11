package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexContext is what a Codex session file has said so far, kept between
// scans so the file can be read a piece at a time.
type codexContext struct {
	Session    string            `json:"s,omitempty"`
	Cwd        string            `json:"p,omitempty"`
	Via        string            `json:"via,omitempty"`
	Acct       string            `json:"acct,omitempty"`
	Plan       string            `json:"plan,omitempty"`
	Model      string            `json:"m,omitempty"`     // the latest turn's model
	Turns      map[string]string `json:"turns,omitempty"` // turn id -> model (recent turns)
	Records    bool              `json:"rec,omitempty"`   // has per-call usage records
	LastTotal  int64             `json:"last,omitempty"`  // older logs: the session's running total
	TurnsOrder []string          `json:"order,omitempty"`
}

func (x *codexContext) remember(turn, model string) {
	if x.Turns == nil {
		x.Turns = map[string]string{}
	}
	if _, ok := x.Turns[turn]; !ok {
		x.TurnsOrder = append(x.TurnsOrder, turn)
		if len(x.TurnsOrder) > 64 {
			delete(x.Turns, x.TurnsOrder[0])
			x.TurnsOrder = x.TurnsOrder[1:]
		}
	}
	x.Turns[turn] = model
}

var markCodexUsage = [][]byte{[]byte(`"session_meta"`), []byte(`"turn_context"`), []byte(`"token_usage_record"`), []byte(`"token_count"`)}

func interesting(line []byte) bool {
	for _, m := range markCodexUsage {
		if bytes.Contains(line, m) {
			return true
		}
	}
	return false
}

// readCodex reads a Codex session log from byte offset from: one call per
// model response (token_usage_record), numbers only. The model comes from the
// turn it belongs to; the plan from the token_count that follows each call.
// Logs from before per-call records existed are read from their running
// totals instead.
func readCodex(path string, from int64, loc *time.Location, x *codexContext) ([]Call, int64, error) {
	var calls, older []Call
	off, err := readLines(path, from, func(line []byte) {
		if !interesting(line) {
			return
		}
		var l codexLine
		if json.Unmarshal(line, &l) != nil {
			return
		}
		switch l.Type {
		case "session_meta":
			var p struct {
				ID               string `json:"id"`
				SessionID        string `json:"session_id"`
				Cwd              string `json:"cwd"`
				Originator       string `json:"originator"`
				CreatorAccountID string `json:"creator_account_id"`
			}
			if json.Unmarshal(l.Payload, &p) == nil {
				x.Session = firstOf(p.SessionID, p.ID, x.Session)
				x.Cwd, x.Via = firstOf(p.Cwd, x.Cwd), firstOf(p.Originator, x.Via)
				if p.CreatorAccountID != "" {
					x.Acct = AccountID("openai", p.CreatorAccountID)
				}
			}
		case "turn_context":
			var p struct {
				TurnID string `json:"turn_id"`
				Model  string `json:"model"`
				Cwd    string `json:"cwd"`
			}
			if json.Unmarshal(l.Payload, &p) == nil && p.Model != "" {
				x.Model = p.Model
				if p.TurnID != "" {
					x.remember(p.TurnID, p.Model)
				}
				x.Cwd = firstOf(p.Cwd, x.Cwd)
			}
		case "token_usage_record":
			var p struct {
				ResponseID string      `json:"response_id"`
				SessionID  string      `json:"session_id"`
				TurnID     string      `json:"turn_id"`
				Usage      codexTokens `json:"usage"`
			}
			t, ok := parseTime(l.Timestamp)
			if json.Unmarshal(l.Payload, &p) != nil || !ok || p.ResponseID == "" {
				return
			}
			x.Records = true
			model := x.Turns[p.TurnID]
			if model == "" {
				model = x.Model
			}
			calls = append(calls, codexCall(Key("codex", p.ResponseID), t, loc, firstOf(p.SessionID, x.Session), model, p.Usage, x))
		case "event_msg":
			var p struct {
				Type string `json:"type"`
				Info *struct {
					Last  *codexTokens `json:"last_token_usage"`
					Total *codexTokens `json:"total_token_usage"`
				} `json:"info"`
				RateLimits *struct {
					PlanType string `json:"plan_type"`
				} `json:"rate_limits"`
			}
			if json.Unmarshal(l.Payload, &p) != nil || p.Type != "token_count" {
				return
			}
			if p.RateLimits != nil && p.RateLimits.PlanType != "" {
				x.Plan = p.RateLimits.PlanType
				// The plan arrives just after the call it belongs to.
				if n := len(calls); n > 0 && calls[n-1].Plan == "" {
					calls[n-1].Plan, calls[n-1].AK = x.Plan, codexKind(x.Plan)
				}
			}
			// Older logs: no per-call records, only running totals.
			if p.Info != nil && p.Info.Last != nil && p.Info.Total != nil && p.Info.Total.Total != x.LastTotal {
				x.LastTotal = p.Info.Total.Total
				if t, ok := parseTime(l.Timestamp); ok {
					older = append(older, codexCall(Key("codex", fmt.Sprintf("%s:%d", x.Session, p.Info.Total.Total)), t, loc, x.Session, x.Model, *p.Info.Last, x))
				}
			}
		}
	})
	if !x.Records {
		calls = append(calls, older...)
	}
	return calls, off, err
}

func codexCall(k string, t time.Time, loc *time.Location, session, model string, u codexTokens, x *codexContext) Call {
	in := u.Input - u.Cached - u.CacheWrite
	if in < 0 {
		in = 0
	}
	c := Call{V: 1, K: k, A: "codex", Via: x.Via, T: t.Unix(), D: t.In(loc).Format("2006-01-02"),
		S: session, P: x.Cwd, M: model, Acct: x.Acct, Plan: x.Plan, In: in, CR: u.Cached, CW5: u.CacheWrite,
		Out: u.Output, Rsn: u.Reasoning}
	c.AK = codexKind(c.Plan)
	if c.Acct != "" {
		c.ASrc = "session"
	} else {
		c.ASrc = "none"
	}
	return c
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
