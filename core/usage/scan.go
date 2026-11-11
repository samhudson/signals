package usage

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samhudson/signals/core/engine"
)

// checkpoint is where the ledger stopped reading one agent log file.
type checkpoint struct {
	Size  int64         `json:"size"`
	MTime int64         `json:"mtime"`
	Off   int64         `json:"off"`
	Head  string        `json:"head"`
	Codex *codexContext `json:"codex,omitempty"`
}

type checkpoints struct {
	V     int                    `json:"v"`
	First int64                  `json:"first"`           // first scan: calls before it are backfill
	Last  int64                  `json:"last"`            // last scan
	Login string                 `json:"login,omitempty"` // Claude account at the last scan (hashed)
	Files map[string]*checkpoint `json:"files"`           // by hash of the path
}

// ScanOptions tune a scan; zero values are sensible.
type ScanOptions struct {
	Now     time.Time
	Budget  time.Duration // stop after this long (the next scan carries on)
	Pricing *Pricing
	Gemini  bool // read Gemini CLI chats (not verified yet)
}

// Stats says what a scan did.
type Stats struct {
	Files    int    `json:"files"`    // log files looked at
	Read     int    `json:"read"`     // files read (changed since last time)
	Calls    int    `json:"calls"`    // new calls recorded
	Complete bool   `json:"complete"` // false if the time budget ran out
	Busy     bool   `json:"busy,omitempty"`
	Error    string `json:"error,omitempty"`
}

type logFile struct {
	agent, path string
	size, mtime int64
}

func listLogs(gemini bool) []logFile {
	var out []logFile
	walk := func(agent, root string, match func(string) bool) {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !match(d.Name()) {
				return nil
			}
			if info, err := d.Info(); err == nil {
				out = append(out, logFile{agent, p, info.Size(), info.ModTime().UnixNano()})
			}
			return nil
		})
	}
	walk("claude", claudeProjects(), func(n string) bool { return strings.HasSuffix(n, ".jsonl") })
	walk("codex", codexSessions(), func(n string) bool { return strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, ".jsonl") })
	sort.Slice(out, func(i, j int) bool { return out[i].mtime > out[j].mtime })
	return out
}

// Scan reads what the agents wrote since the last scan into the ledger.
func (l *Ledger) Scan(o ScanOptions) (Stats, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Budget == 0 {
		o.Budget = 3 * time.Second
	}
	if o.Pricing == nil {
		p, err := LoadPricing(filepath.Join(filepath.Dir(l.Dir), "pricing.json"))
		if err != nil {
			return Stats{}, err
		}
		o.Pricing = p
	}
	unlock, err := l.lock(o.Now, engine.Alive)
	if err != nil {
		return Stats{Busy: true}, err
	}
	defer unlock()
	start := time.Now()

	cpPath := filepath.Join(l.Dir, "checkpoints.json")
	cks := checkpoints{V: 1, Files: map[string]*checkpoint{}}
	if data, err := os.ReadFile(cpPath); err == nil {
		json.Unmarshal(data, &cks)
		if cks.Files == nil {
			cks.Files = map[string]*checkpoint{}
		}
	}
	firstScan := cks.First == 0
	if firstScan {
		cks.First = o.Now.Unix()
	}
	// The Claude account is whoever is signed in now. If that changed since the
	// last scan, calls in between can't be told apart: they get no account.
	login, kind := claudeLogin()
	switched := !firstScan && cks.Login != login
	cutoff := o.Now.In(l.Loc).AddDate(0, 0, -callDays).Format("2006-01-02")

	st := Stats{Complete: true}
	seen := map[string]map[string]bool{}
	rollups := map[string]*Rollup{}
	rollup := func(day string) *Rollup {
		m := day[:7]
		if r, ok := rollups[m]; ok {
			return r
		}
		r := l.loadRollup(m, o.Pricing)
		rollups[m] = r
		return r
	}

	for _, f := range listLogs(o.Gemini) {
		st.Files++
		key := hash16(f.path)
		ck := cks.Files[key]
		if ck != nil && ck.Size == f.size && ck.MTime == f.mtime {
			continue
		}
		if time.Since(start) > o.Budget {
			st.Complete = false
			break
		}
		isNew := ck == nil
		if ck == nil {
			ck = &checkpoint{}
		}
		head := headHash(f.path)
		from := ck.Off
		if f.size < ck.Off || (ck.Head != "" && head != ck.Head) {
			from, ck.Codex = 0, nil // replaced or rewritten: read again; dedupe keeps counts right
		}
		var calls []Call
		var off int64
		switch f.agent {
		case "claude":
			calls, _, off, err = readClaude(f.path, from, l.Loc, false)
			for i := range calls {
				c := &calls[i]
				switch {
				case switched && c.T > cks.Last:
					c.Acct, c.AK, c.ASrc = "", "unknown", "none"
				case login == "":
					c.AK, c.ASrc = "unknown", "none"
				default:
					c.Acct, c.AK = login, kind
					if c.T >= cks.First {
						c.ASrc = "login"
					} else {
						c.ASrc = "backfill"
					}
				}
			}
		case "codex":
			if ck.Codex == nil {
				ck.Codex = &codexContext{}
			}
			calls, off, err = readCodex(f.path, from, l.Loc, ck.Codex)
		}
		if err != nil {
			continue
		}
		st.Read++
		// Calls older than the records are kept are only taken the first time
		// a log is seen and that day has no totals yet (a backfill), never
		// again from a resumed session.
		var keep []Call
		for _, c := range calls {
			if c.D < cutoff && !(isNew && !rollup(c.D).hasDay(c.D, c.A)) {
				continue
			}
			keep = append(keep, c)
		}
		added, aerr := l.appendCalls(keep, seen)
		for _, c := range added {
			rollup(c.D).row(c).add(c, o.Pricing)
		}
		st.Calls += len(added)
		if aerr != nil {
			st.Error = aerr.Error()
			break
		}
		ck.Size, ck.MTime, ck.Off, ck.Head = f.size, f.mtime, off, head
		cks.Files[key] = ck
	}

	for _, r := range rollups {
		if err := l.saveRollup(r); err != nil {
			return st, err
		}
	}
	cks.Last, cks.Login = o.Now.Unix(), login
	if err := writeJSON(cpPath, cks); err != nil {
		return st, err
	}
	l.prune(o.Now)
	writeJSON(filepath.Join(l.Dir, "status.json"), map[string]any{"at": o.Now.Unix(), "stats": st, "took_ms": time.Since(start).Milliseconds()})
	return st, nil
}
