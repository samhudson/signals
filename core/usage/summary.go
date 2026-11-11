package usage

import (
	"sort"
	"time"
)

// Names of the agents, as people know them.
var agentNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI"}

func agentName(a string) string {
	if n, ok := agentNames[a]; ok {
		return n
	}
	return a
}

// Tokens splits a total by kind.
type Tokens struct {
	In         int64 `json:"in"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Out        int64 `json:"out"`
	Total      int64 `json:"total"`
}

func (t *Tokens) addRow(r Row) {
	t.In += r.In
	t.CacheRead += r.CR
	t.CacheWrite += r.CW5 + r.CW1h
	t.Out += r.Out
	t.Total += r.Tokens()
}

// Tool is one agent's month.
type Tool struct {
	Agent    string   `json:"agent"`
	Name     string   `json:"name"`
	Calls    int64    `json:"calls"`
	Cost     float64  `json:"cost"`
	Tokens   Tokens   `json:"tokens"`
	Unpriced Unpriced `json:"unpriced"`
}

// Unpriced counts calls whose model has no price yet (counted, not guessed).
type Unpriced struct {
	Calls  int64    `json:"calls"`
	Models []string `json:"models,omitempty"`
}

// Session is one agent session today (stays on this computer).
type Session struct {
	Agent  string  `json:"agent"`
	Name   string  `json:"name"`
	SID    string  `json:"sid"`
	Calls  int64   `json:"calls"`
	Cost   float64 `json:"cost"`
	Last   int64   `json:"last"`
	Cwd    string  `json:"cwd,omitempty"`
	Priced bool    `json:"priced"`
}

// Group is a total by model or by day (for --by).
type Group struct {
	Key    string  `json:"key"`
	Agent  string  `json:"agent,omitempty"`
	Calls  int64   `json:"calls"`
	Cost   float64 `json:"cost"`
	Tokens int64   `json:"tokens"`
}

// Summary is a month of usage on this computer, by agent.
type Summary struct {
	V        int       `json:"v"`
	Month    string    `json:"month"`
	Label    string    `json:"label"`
	AsOf     int64     `json:"as_of"`
	Complete bool      `json:"complete"`
	Pricing  string    `json:"pricing"` // the price table's date
	Tools    []Tool    `json:"tools"`
	Total    Tool      `json:"total"`
	Today    string    `json:"today"`
	Sessions []Session `json:"sessions"` // today's, newest first
	By       string    `json:"by,omitempty"`
	Groups   []Group   `json:"groups,omitempty"`
}

// Summarize totals a month from the ledger. by is "", "model" or "day".
func (l *Ledger) Summarize(month, by string, now time.Time, p *Pricing, complete bool) Summary {
	s := Summary{V: 1, Month: month, Label: "API-price estimate", AsOf: now.Unix(), Complete: complete,
		Pricing: p.Updated, Today: now.In(l.Loc).Format("2006-01-02"), Tools: []Tool{}, Sessions: []Session{}, By: by}
	r := l.loadRollup(month, p)
	tools := map[string]*Tool{}
	unpriced := map[string]map[string]bool{}
	groups := map[string]*Group{}
	for _, row := range r.Rows {
		t := tools[row.A]
		if t == nil {
			t = &Tool{Agent: row.A, Name: agentName(row.A)}
			tools[row.A] = t
		}
		t.Calls += row.Calls
		t.Cost += row.Cost
		t.Tokens.addRow(row)
		if row.Unpriced > 0 {
			t.Unpriced.Calls += row.Unpriced
			if unpriced[row.A] == nil {
				unpriced[row.A] = map[string]bool{}
			}
			unpriced[row.A][row.M] = true
		}
		var k string
		switch by {
		case "model":
			k = row.A + "|" + row.M
		case "day":
			k = row.D
		}
		if k != "" {
			g := groups[k]
			if g == nil {
				g = &Group{Key: k}
				if by == "model" {
					g.Key, g.Agent = row.M, row.A
				}
				groups[k] = g
			}
			g.Calls += row.Calls
			g.Cost += row.Cost
			g.Tokens += row.Tokens()
		}
	}
	for a, t := range tools {
		for m := range unpriced[a] {
			t.Unpriced.Models = append(t.Unpriced.Models, m)
		}
		sort.Strings(t.Unpriced.Models)
		s.Tools = append(s.Tools, *t)
		s.Total.Calls += t.Calls
		s.Total.Cost += t.Cost
		s.Total.Tokens.In += t.Tokens.In
		s.Total.Tokens.CacheRead += t.Tokens.CacheRead
		s.Total.Tokens.CacheWrite += t.Tokens.CacheWrite
		s.Total.Tokens.Out += t.Tokens.Out
		s.Total.Tokens.Total += t.Tokens.Total
		s.Total.Unpriced.Calls += t.Unpriced.Calls
	}
	s.Total.Agent, s.Total.Name = "all", "Total"
	sort.Slice(s.Tools, func(i, j int) bool { return s.Tools[i].Calls > s.Tools[j].Calls })
	for _, g := range groups {
		s.Groups = append(s.Groups, *g)
	}
	sort.Slice(s.Groups, func(i, j int) bool {
		if by == "day" {
			return s.Groups[i].Key < s.Groups[j].Key
		}
		return s.Groups[i].Cost > s.Groups[j].Cost
	})

	// Today's sessions, from today's call records.
	sessions := map[string]*Session{}
	l.eachCall(s.Today, func(c Call) {
		k := c.A + "|" + c.S
		x := sessions[k]
		if x == nil {
			x = &Session{Agent: c.A, Name: agentName(c.A), SID: c.S, Priced: true}
			sessions[k] = x
		}
		x.Calls++
		if cost, ok := p.Cost(c); ok {
			x.Cost += cost
		} else {
			x.Priced = false
		}
		if c.T > x.Last {
			x.Last = c.T
		}
		if c.P != "" {
			x.Cwd = c.P
		}
	})
	for _, x := range sessions {
		s.Sessions = append(s.Sessions, *x)
	}
	sort.Slice(s.Sessions, func(i, j int) bool { return s.Sessions[i].Last > s.Sessions[j].Last })
	return s
}
