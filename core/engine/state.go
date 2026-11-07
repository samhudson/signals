package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Row is one session as the menu bar, tray and phone show it.
type Row struct {
	Agent     string  `json:"agent"`
	Who       string  `json:"who"`
	Status    string  `json:"status"`
	Label     string  `json:"label"`
	Name      string  `json:"name"`
	Cwd       string  `json:"cwd"`
	Updated   int64   `json:"updated"`
	Detail    *string `json:"detail"`
	SessionID string  `json:"session_id"`
	Why       string  `json:"why"`
}

// State is every agent session, with counts by status.
type State struct {
	Now    int64          `json:"now"`
	Rows   []Row          `json:"rows"`
	Counts map[string]int `json:"counts"`
}

type session struct {
	Agent, SessionID, Status, Cwd, Title, Transcript, Detail string
	Updated                                                  int64
	tag, why                                                 string
}

// registryEntry is Claude Code's own record of a running session
// (~/.claude/sessions/<pid>.json): status busy, idle or waiting.
type registryEntry struct {
	Pid             int     `json:"pid"`
	SessionID       string  `json:"sessionId"`
	Cwd             string  `json:"cwd"`
	Status          string  `json:"status"`
	StatusUpdatedAt float64 `json:"statusUpdatedAt"`
	StartedAt       float64 `json:"startedAt"`
	Name            *string `json:"name"`
}

var labels = map[string]string{"working": "working…", "waiting": "needs you", "done": "done", "error": "stopped on an error"}
var whoNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini", "cursor": "Cursor", "copilot": "Copilot"}
var rank = map[string]int{"waiting": 0, "working": 1, "error": 2, "done": 3}

// Sessions merges the hook state with Claude Code's registry, prunes sessions
// that ended, corrects Claude turns that were interrupted (Esc fires no hook),
// names each session, and explains each light.
func Sessions(now time.Time) State {
	t := now.Unix()
	registry, rawRegistry := readRegistry()

	// Prune: Claude sessions whose process exited; anything silent for 12h.
	files, _ := filepath.Glob(filepath.Join(SessionsDir(), "*.json"))
	var states []session
	for _, f := range files {
		s, ok := readSession(f)
		if !ok {
			continue
		}
		if s.Agent == "claude" {
			if _, live := registry[s.SessionID]; !live && bytes.Contains(rawRegistry, []byte(`"sessionId":"`+s.SessionID+`"`)) {
				os.Remove(f)
				continue
			}
		}
		if t-s.Updated > 43200 {
			if _, live := registry[s.SessionID]; !(s.Agent == "claude" && live) {
				os.Remove(f)
				continue
			}
		}
		states = append(states, s)
	}

	var all []session
	tracked := map[string]bool{}
	for _, s := range states {
		var r *registryEntry
		if s.Agent == "claude" {
			if e, ok := registry[s.SessionID]; ok {
				r = &e
			}
			tracked[s.SessionID] = true
		}
		rt := int64(0)
		if r != nil {
			rt = int64(r.StatusUpdatedAt / 1000)
		}
		s.tag = short(s.SessionID)
		if r != nil && r.Name != nil {
			s.tag = *r.Name
		}
		title := tidy(s.Title)
		if s.Agent == "claude" {
			if own := claudeTitle(s.SessionID); own != "" {
				title = own
			}
		}
		s.Title = title
		s.why = fmt.Sprintf("last hook event: %s at %s", s.Status, at(s.Updated))
		if r != nil {
			s.why += fmt.Sprintf(" · Claude registry: %s at %s", r.Status, at(rt))
		}
		if s.Agent == "claude" && (s.Status == "working" || s.Status == "waiting") {
			if it := interruptedAt(s); it > s.Updated {
				s.Status, s.Updated, s.Detail = "done", it, "interrupted"
				s.why += fmt.Sprintf(" · interrupted at %s, so done", at(it))
			} else if r != nil && r.Status == "idle" && rt > s.Updated+1 {
				s.Status, s.Updated = "done", rt
				s.why += " · idle since, so the turn ended (likely Esc)"
			}
		}
		all = append(all, s)
	}
	// Claude sessions started before the hooks were installed: use the registry.
	for _, e := range registry {
		if tracked[e.SessionID] {
			continue
		}
		status := "done"
		if e.Status == "busy" {
			status = "working"
		}
		updated := e.StatusUpdatedAt
		if updated == 0 {
			updated = e.StartedAt
		}
		tag := short(e.SessionID)
		if e.Name != nil {
			tag = *e.Name
		}
		all = append(all, session{Agent: "claude", SessionID: e.SessionID, Cwd: e.Cwd, Status: status,
			Updated: int64(updated / 1000), Title: claudeTitle(e.SessionID), tag: tag,
			why: "no hook events yet (started before Signals was set up) · Claude registry: " + e.Status})
	}

	sort.SliceStable(all, func(i, j int) bool {
		ri, rj := rankOf(all[i].Status), rankOf(all[j].Status)
		if ri != rj {
			return ri < rj
		}
		return all[i].Updated > all[j].Updated
	})

	// Name = folder; parent too when two share a folder name; session tag when
	// two sessions of the same agent are in the very same folder.
	names, paths := map[string]int{}, map[string]int{}
	for _, s := range all {
		names[last(s.Cwd)]++
		paths[s.Agent+":"+s.Cwd]++
	}
	out := State{Now: t, Rows: []Row{}, Counts: map[string]int{}}
	for _, s := range all {
		folder := last(s.Cwd)
		if names[folder] > 1 {
			folder = lastTwo(s.Cwd)
		}
		name := folder
		switch {
		case s.Title != "":
			name = clipRunes(s.Title)
		case paths[s.Agent+":"+s.Cwd] > 1:
			name = folder + " [" + s.tag + "]"
		}
		row := Row{Agent: s.Agent, Who: orDefault(whoNames, s.Agent), Status: s.Status, Label: orDefault(labels, s.Status),
			Name: name, Cwd: s.Cwd, Updated: s.Updated, SessionID: s.SessionID, Why: s.why}
		if s.Detail != "" {
			d := s.Detail
			row.Detail = &d
		}
		out.Rows = append(out.Rows, row)
		out.Counts[s.Status]++
	}
	return out
}

func readRegistry() (map[string]registryEntry, []byte) {
	live := map[string]registryEntry{}
	var raw []byte
	files, _ := filepath.Glob(filepath.Join(ClaudeDir(), "sessions", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		raw = append(raw, data...)
		var e registryEntry
		if json.Unmarshal(data, &e) == nil && e.SessionID != "" && alive(e.Pid) {
			if _, seen := live[e.SessionID]; !seen {
				live[e.SessionID] = e
			}
		}
	}
	return live, raw
}

func readSession(f string) (session, bool) {
	data, err := os.ReadFile(f)
	if err != nil {
		return session{}, false
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return session{}, false
	}
	s := session{Agent: str(m, "agent"), SessionID: str(m, "session_id"), Status: str(m, "status"), Cwd: str(m, "cwd"),
		Title: str(m, "title"), Transcript: str(m, "transcript"), Detail: str(m, "detail")}
	if u, ok := m["updated"].(float64); ok {
		s.Updated = int64(u)
	}
	return s, true
}

// transcript finds a Claude session's transcript.
func transcript(sid, known string) string {
	if known != "" {
		if _, err := os.Stat(known); err == nil {
			return known
		}
	}
	m, _ := filepath.Glob(filepath.Join(ClaudeDir(), "projects", "*", sid+".jsonl"))
	if len(m) > 0 {
		return m[0]
	}
	return ""
}

// claudeTitle is Claude Code's own title (the one in the editor tab and
// /resume): a renamed title wins over the AI-generated one.
func claudeTitle(sid string) string {
	t := transcript(sid, "")
	if t == "" {
		return ""
	}
	f, err := os.Open(t)
	if err != nil {
		return ""
	}
	defer f.Close()
	var custom, ai string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case bytes.Contains(line, []byte(`"type":"custom-title"`)):
			var e struct{ CustomTitle, Title string }
			if json.Unmarshal(line, &e) == nil {
				custom = firstNonEmpty(e.CustomTitle, e.Title)
			}
		case bytes.Contains(line, []byte(`"type":"ai-title"`)):
			var e struct{ AiTitle string }
			if json.Unmarshal(line, &e) == nil {
				ai = e.AiTitle
			}
		}
	}
	return firstNonEmpty(custom, ai)
}

// interruptedAt is when the user last pressed Esc in a Claude session: Esc
// fires no hook, but Claude Code writes a "[Request interrupted by user" entry.
// Only a user entry whose text starts with it counts (the same words inside a
// message or tool call don't).
func interruptedAt(s session) int64 {
	t := transcript(s.SessionID, s.Transcript)
	if t == "" {
		return 0
	}
	f, err := os.Open(t)
	if err != nil {
		return 0
	}
	defer f.Close()
	const window = 400000
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		f.Seek(fi.Size()-window, 0)
	}
	var latest int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte("[Request interrupted by user")) {
			continue
		}
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "user" || !startsInterrupt(e.Message.Content) {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
			latest = ts.Unix()
		}
	}
	return latest
}

func startsInterrupt(content json.RawMessage) bool {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return strings.HasPrefix(text, "[Request interrupted by user")
	}
	var parts []map[string]any
	if json.Unmarshal(content, &parts) == nil {
		for _, p := range parts {
			if s, _ := p["text"].(string); strings.HasPrefix(s, "[Request interrupted by user") {
				return true
			}
		}
	}
	return false
}

// tidy drops titles saved before editor context was stripped from prompts.
func tidy(t string) string {
	if strings.HasPrefix(t, "<ide_") || strings.HasPrefix(t, "<system-reminder") {
		return ""
	}
	return t
}

func at(t int64) string { return time.Unix(t, 0).Local().Format("15:04:05") }

func short(sid string) string {
	if len(sid) > 6 {
		return sid[:6]
	}
	return sid
}

// Folder names work for both macOS/Linux (/) and Windows (\) paths.
func pathParts(p string) []string { return strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") }

func last(p string) string {
	parts := pathParts(p)
	return parts[len(parts)-1]
}

func lastTwo(p string) string {
	parts := pathParts(p)
	if len(parts) < 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func rankOf(status string) int {
	if r, ok := rank[status]; ok {
		return r
	}
	return 4
}

func orDefault(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
