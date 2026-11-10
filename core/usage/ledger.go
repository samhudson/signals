package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	callDays     = 62 // per-call records are kept this long
	rollupMonths = 25 // daily totals are kept this long
	staleLock    = 5 * time.Minute
)

// Ledger is the usage ledger on this computer (~/.signals/usage/ledger).
type Ledger struct {
	Dir string
	Loc *time.Location
}

// Open returns the ledger in dir; days are counted in loc.
func Open(dir string, loc *time.Location) *Ledger {
	if loc == nil {
		loc = time.Local
	}
	return &Ledger{Dir: dir, Loc: loc}
}

func (l *Ledger) callsFile(day string) string { return filepath.Join(l.Dir, "calls", day+".jsonl") }
func (l *Ledger) rollupFile(month string) string {
	return filepath.Join(l.Dir, "rollups", month+".json")
}

// Row is one day's totals for one agent, model and account.
type Row struct {
	D        string  `json:"d"`
	A        string  `json:"a"`
	M        string  `json:"m"`
	Acct     string  `json:"acct,omitempty"`
	AK       string  `json:"ak,omitempty"`
	Plan     string  `json:"plan,omitempty"`
	Calls    int64   `json:"calls"`
	In       int64   `json:"in"`
	CR       int64   `json:"cr"`
	CW5      int64   `json:"cw5"`
	CW1h     int64   `json:"cw1h"`
	Out      int64   `json:"out"`
	Rsn      int64   `json:"rsn,omitempty"`
	WS       int64   `json:"ws,omitempty"`
	Cost     float64 `json:"cost"`
	Unpriced int64   `json:"unpriced,omitempty"` // calls whose model has no price
}

func (r Row) Tokens() int64 { return r.In + r.CR + r.CW5 + r.CW1h + r.Out }

func rowKey(c Call) string {
	return strings.Join([]string{c.D, c.A, c.M, c.Acct, c.AK, c.Plan}, "|")
}

func (r *Row) add(c Call, p *Pricing) {
	r.Calls++
	r.In += c.In
	r.CR += c.CR
	r.CW5 += c.CW5
	r.CW1h += c.CW1h
	r.Out += c.Out
	r.Rsn += c.Rsn
	r.WS += c.WS
	if cost, ok := p.Cost(c); ok {
		r.Cost += cost
	} else {
		r.Unpriced++
	}
}

// Rollup is a month of daily totals, priced with one version of the prices.
type Rollup struct {
	V     int    `json:"v"`
	Month string `json:"month"`
	PH    string `json:"ph"` // which prices
	Rows  []Row  `json:"rows"`
	index map[string]int
}

func (r *Rollup) row(c Call) *Row {
	if r.index == nil {
		r.index = map[string]int{}
		for i, row := range r.Rows {
			r.index[strings.Join([]string{row.D, row.A, row.M, row.Acct, row.AK, row.Plan}, "|")] = i
		}
	}
	k := rowKey(c)
	if i, ok := r.index[k]; ok {
		return &r.Rows[i]
	}
	r.Rows = append(r.Rows, Row{D: c.D, A: c.A, M: c.M, Acct: c.Acct, AK: c.AK, Plan: c.Plan})
	r.index[k] = len(r.Rows) - 1
	return &r.Rows[len(r.Rows)-1]
}

func (r *Rollup) hasDay(day, agent string) bool {
	for _, row := range r.Rows {
		if row.D == day && row.A == agent {
			return true
		}
	}
	return false
}

// loadRollup reads a month's totals. If they were priced with other prices,
// the days whose call records are still kept are priced again.
func (l *Ledger) loadRollup(month string, p *Pricing) *Rollup {
	r := &Rollup{V: 1, Month: month}
	if data, err := os.ReadFile(l.rollupFile(month)); err == nil {
		json.Unmarshal(data, r)
		r.index = nil
	}
	if r.PH == "" {
		r.PH = p.Hash()
	}
	if r.PH != p.Hash() {
		l.reprice(r, p)
	}
	return r
}

func (l *Ledger) reprice(r *Rollup, p *Pricing) {
	days := map[string]bool{}
	for _, row := range r.Rows {
		days[row.D] = true
	}
	var keep []Row
	var redo []string
	for _, row := range r.Rows {
		if _, err := os.Stat(l.callsFile(row.D)); err == nil {
			continue
		}
		keep = append(keep, row)
	}
	for d := range days {
		if _, err := os.Stat(l.callsFile(d)); err == nil {
			redo = append(redo, d)
		}
	}
	r.Rows, r.index, r.PH = keep, nil, p.Hash()
	sort.Strings(redo)
	for _, d := range redo {
		l.eachCall(d, func(c Call) { r.row(c).add(c, p) })
	}
}

func (l *Ledger) saveRollup(r *Rollup) error {
	sort.SliceStable(r.Rows, func(i, j int) bool {
		a, b := r.Rows[i], r.Rows[j]
		if a.D != b.D {
			return a.D < b.D
		}
		if a.A != b.A {
			return a.A < b.A
		}
		return a.M < b.M
	})
	r.index = nil
	return writeJSON(l.rollupFile(r.Month), r)
}

// eachCall calls fn for every call recorded on a day.
func (l *Ledger) eachCall(day string, fn func(Call)) {
	readLines(l.callsFile(day), 0, func(line []byte) {
		var c Call
		if json.Unmarshal(line, &c) == nil {
			fn(c)
		}
	})
}

// Calls returns the calls recorded from day since to day until (inclusive).
func (l *Ledger) Calls(since, until string) []Call {
	files, _ := filepath.Glob(filepath.Join(l.Dir, "calls", "*.jsonl"))
	sort.Strings(files)
	var out []Call
	for _, f := range files {
		d := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if d < since || (until != "" && d > until) {
			continue
		}
		l.eachCall(d, func(c Call) { out = append(out, c) })
	}
	return out
}

// appendCalls writes new calls to their day files, skipping any already
// recorded, and returns the ones that were new.
func (l *Ledger) appendCalls(calls []Call, seen map[string]map[string]bool) ([]Call, error) {
	byDay := map[string][]Call{}
	for _, c := range calls {
		byDay[c.D] = append(byDay[c.D], c)
	}
	var added []Call
	for day, cs := range byDay {
		keys, ok := seen[day]
		if !ok {
			keys = map[string]bool{}
			l.eachCall(day, func(c Call) { keys[c.K] = true })
			seen[day] = keys
		}
		var fresh []Call
		for _, c := range cs {
			if !keys[c.K] {
				keys[c.K] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(l.callsFile(day)), 0o755); err != nil {
			return added, err
		}
		f, err := os.OpenFile(l.callsFile(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return added, err
		}
		w := bufio.NewWriter(f)
		for _, c := range fresh {
			b, _ := json.Marshal(c)
			w.Write(b)
			w.WriteByte('\n')
		}
		err = w.Flush()
		f.Close()
		if err != nil {
			return added, err
		}
		added = append(added, fresh...)
	}
	return added, nil
}

// prune deletes call records and monthly totals older than they're kept.
func (l *Ledger) prune(now time.Time) {
	cutoff := now.In(l.Loc).AddDate(0, 0, -callDays).Format("2006-01-02")
	files, _ := filepath.Glob(filepath.Join(l.Dir, "calls", "*.jsonl"))
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), ".jsonl") < cutoff {
			os.Remove(f)
		}
	}
	mcut := now.In(l.Loc).AddDate(0, -rollupMonths, 0).Format("2006-01")
	months, _ := filepath.Glob(filepath.Join(l.Dir, "rollups", "*.json"))
	for _, f := range months {
		if strings.TrimSuffix(filepath.Base(f), ".json") < mcut {
			os.Remove(f)
		}
	}
}

// lock makes sure only one scan runs at a time. A lock whose process is gone,
// or that is older than five minutes, is taken over.
var ErrBusy = errors.New("another scan is running")

func (l *Ledger) lock(now time.Time, alive func(int) bool) (func(), error) {
	path := filepath.Join(l.Dir, "lock")
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %d", os.Getpid(), now.Unix())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		data, _ := os.ReadFile(path)
		var pid int
		var at int64
		fmt.Sscanf(string(data), "%d %d", &pid, &at)
		if pid > 0 && alive(pid) && now.Sub(time.Unix(at, 0)) < staleLock {
			return nil, ErrBusy
		}
		os.Remove(path)
	}
	return nil, ErrBusy
}

// writeJSON writes a file atomically (a crash leaves the old one).
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
