package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/samhudson/signals/core/engine"
)

const cliHelp = `usage: signals usage [summary] [--json] [--month YYYY-MM] [--by agent|model|day] [--no-scan]
       signals usage ledger [--since YYYY-MM-DD] [--agent claude|codex]
       signals usage verify [--json]
       signals usage scan

Counts every API call your coding agents make, from the logs they already keep
on this computer (Claude Code and Codex), priced at each vendor's list prices.
Numbers only: no prompts, replies or code are read or kept.
`

type cliFlags struct {
	json, noScan            bool
	month, by, since, agent string
}

func parseFlags(args []string) (cliFlags, error) {
	var f cliFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--json":
			f.json = true
		case "--no-scan":
			f.noScan = true
		case "--month":
			f.month, err = val()
		case "--by":
			f.by, err = val()
		case "--since":
			f.since, err = val()
		case "--agent":
			f.agent, err = val()
		default:
			err = fmt.Errorf("unknown option %s", a)
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// CLI runs `signals usage ...` and returns the exit code.
func CLI(args []string, stdout, stderr io.Writer) int {
	cmd := "summary"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	f, err := parseFlags(args)
	if err != nil || cmd == "help" {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprint(stderr, cliHelp)
		return 2
	}
	base := filepath.Join(engine.SignalsHome(), "usage")
	p, err := LoadPricing(filepath.Join(base, "pricing.json"))
	if err != nil {
		fmt.Fprintln(stderr, "Couldn't read the price table:", err)
		return 1
	}
	l := Open(filepath.Join(base, "ledger"), time.Local)
	now := time.Now()
	out := json.NewEncoder(stdout)
	out.SetEscapeHTML(false)

	switch cmd {
	case "scan":
		st, _ := l.Scan(ScanOptions{Now: now, Pricing: p})
		out.Encode(st)
	case "summary":
		complete := true
		if !f.noScan {
			st, _ := l.Scan(ScanOptions{Now: now, Pricing: p})
			complete = st.Complete && !st.Busy
		}
		month := f.month
		if month == "" {
			month = now.Format("2006-01")
		}
		by := f.by
		if by == "agent" {
			by = ""
		}
		s := l.Summarize(month, by, now, p, complete)
		if f.json {
			out.Encode(s)
		} else {
			printSummary(stdout, s)
		}
	case "ledger":
		if !f.noScan {
			l.Scan(ScanOptions{Now: now, Pricing: p})
		}
		since := f.since
		if since == "" {
			since = now.Format("2006-01") + "-01"
		}
		for _, c := range l.Calls(since, "") {
			if f.agent == "" || c.A == f.agent {
				out.Encode(c)
			}
		}
	case "verify":
		checks := Verify(p)
		if f.json {
			out.Encode(checks)
		} else {
			printChecks(stdout, checks)
		}
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
		fmt.Fprint(stderr, cliHelp)
		return 2
	}
	return 0
}

func money(v float64) string { return fmt.Sprintf("$%.2f", v) }

// count shortens a large number: 18.2M, 41K.
func count(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func printSummary(w io.Writer, s Summary) {
	t, _ := time.Parse("2006-01", s.Month)
	fmt.Fprintf(w, "%s, from agent logs on this computer\n", t.Format("January 2006"))
	fmt.Fprintf(w, "API-price estimate: what these calls would cost at each vendor's list prices.\n\n")
	if len(s.Tools) == 0 {
		fmt.Fprintln(w, "  No Claude Code or Codex calls this month yet.")
		return
	}
	for _, t := range s.Tools {
		fmt.Fprintf(w, "  %-12s %7s calls  %7s tokens  %10s\n", t.Name, count(t.Calls), count(t.Tokens.Total), money(t.Cost))
	}
	if len(s.Tools) > 1 {
		fmt.Fprintf(w, "  %-12s %7s calls  %7s tokens  %10s\n", "Total", count(s.Total.Calls), count(s.Total.Tokens.Total), money(s.Total.Cost))
	}
	for _, t := range s.Tools {
		if t.Unpriced.Calls > 0 {
			fmt.Fprintf(w, "\n  Not priced yet: %d %s calls (%s). Counted, not guessed.\n", t.Unpriced.Calls, t.Name, strings.Join(t.Unpriced.Models, ", "))
		}
	}
	if len(s.Groups) > 0 {
		fmt.Fprintln(w)
		for _, g := range s.Groups {
			name := g.Key
			if g.Agent != "" {
				name = agentName(g.Agent) + " · " + g.Key
			}
			fmt.Fprintf(w, "  %-34s %7s calls  %10s\n", name, count(g.Calls), money(g.Cost))
		}
	}
	if !s.Complete {
		fmt.Fprintln(w, "\n  (Still reading older logs; totals will grow on the next refresh.)")
	}
}

func printChecks(w io.Writer, checks []Check) {
	byAgent := map[string][2]int{}
	for _, c := range checks {
		n := byAgent[c.Agent]
		n[1]++
		if c.Match {
			n[0]++
		}
		byAgent[c.Agent] = n
	}
	for _, a := range []string{"claude", "codex"} {
		if n, ok := byAgent[a]; ok {
			fmt.Fprintf(w, "%s: %d of %d match the agent's own totals exactly\n", agentName(a), n[0], n[1])
		}
	}
	for _, c := range checks {
		if !c.Match || c.Note != "" {
			fmt.Fprintf(w, "  %s %s: ours %s tokens, theirs %s", agentName(c.Agent), c.Log, count(c.Ours), count(c.Theirs))
			if c.Their > 0 {
				fmt.Fprintf(w, " (%s vs %s)", money(c.Cost), money(c.Their))
			}
			if c.Note != "" {
				fmt.Fprintf(w, ": %s", c.Note)
			}
			fmt.Fprintln(w)
		}
	}
}
