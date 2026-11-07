package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Agents Signals can wire up, in display order.
var Agents = []string{"claude", "codex", "gemini", "cursor", "copilot"}

// Our hook commands, and those of the tools Signals replaced.
var ours = regexp.MustCompile(`signals-hook|trafficlight-hook|agentlights-hook|session-status\.sh|tollbooth-gate|signals(\.exe)?["']? hook `)

func AgentName(a string) string {
	return map[string]string{"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "cursor": "Cursor", "copilot": "Copilot (VS Code)"}[a]
}

func agentRoot(a string) string { return filepath.Join(Home(), "."+a) }

// ConfigFile is where an agent reads its hooks.
func ConfigFile(a string) string {
	switch a {
	case "claude", "gemini":
		return filepath.Join(agentRoot(a), "settings.json")
	case "codex", "cursor":
		return filepath.Join(agentRoot(a), "hooks.json")
	case "copilot":
		return filepath.Join(agentRoot(a), "hooks", "signals.json")
	}
	return ""
}

func events(a string) []string {
	return map[string][]string{
		"claude":  {"SessionStart", "UserPromptSubmit", "PostToolUse", "PermissionRequest", "Notification", "Stop", "StopFailure", "SessionEnd"},
		"codex":   {"SessionStart", "UserPromptSubmit", "PostToolUse", "PermissionRequest", "Stop", "Interrupt", "SessionEnd"},
		"gemini":  {"SessionStart", "BeforeAgent", "AfterTool", "Notification", "AfterAgent", "SessionEnd"},
		"cursor":  {"sessionStart", "beforeSubmitPrompt", "postToolUse", "stop", "sessionEnd"},
		"copilot": {"sessionStart", "userPromptSubmitted", "postToolUse", "agentStop", "sessionEnd"},
	}[a]
}

// Detected reports whether an agent seems to be installed.
func Detected(a string) bool {
	if dirExists(agentRoot(a)) {
		return true
	}
	switch a {
	case "claude", "codex", "gemini":
		_, err := exec.LookPath(a)
		return err == nil
	case "cursor":
		return dirExists("/Applications/Cursor.app") || dirExists(filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "cursor"))
	case "copilot":
		m, _ := filepath.Glob(filepath.Join(Home(), ".vscode", "extensions", "github.copilot*"))
		return len(m) > 0
	}
	return false
}

// Installed reports whether an agent has Signals' hooks.
func Installed(a string) bool {
	data, err := os.ReadFile(ConfigFile(a))
	if err != nil {
		return false
	}
	found := false
	walkCommands(decode(data), func(c string) { found = found || ours.MatchString(c) && !strings.Contains(c, "tollbooth") })
	return found
}

// HookCommand is the command an agent runs for one event. On macOS and Linux
// it's guarded, so a removed install exits 0 quietly instead of erroring.
func HookCommand(exe, agent, event string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`"%s" hook %s %s`, filepath.ToSlash(exe), agent, event)
	}
	return fmt.Sprintf(`[ ! -x '%s' ] || '%s' hook %s %s`, exe, exe, agent, event)
}

// Install adds Signals' hooks to an agent's config, keeping everything else.
func Install(a, exe string) error {
	file := ConfigFile(a)
	if !dirExists(agentRoot(a)) {
		// Signals created this agent's folder: remember, so turning it off removes it again.
		os.MkdirAll(filepath.Join(SignalsHome(), "created"), 0o755)
		os.WriteFile(filepath.Join(SignalsHome(), "created", a), nil, 0o644)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if a == "copilot" {
		// Copilot gets a file of its own, so turning it off is deleting it.
		hooks := orderedMap{}
		for _, ev := range events(a) {
			hooks.set(ev, []any{map[string]any{"type": "command", "command": HookCommand(exe, a, ev), "timeoutSec": 5}})
		}
		doc := orderedMap{}
		doc.set("version", 1)
		doc.set("hooks", hooks)
		return writeJSON(file, doc)
	}
	doc, err := readOrdered(file)
	if err != nil {
		return fmt.Errorf("%s isn't valid JSON, so it was left alone: %w", file, err)
	}
	if data, err := os.ReadFile(file); err == nil {
		os.WriteFile(file+".bak.signals", data, 0o644)
	}
	hooks := strip(doc.get("hooks"), a == "cursor")
	timeout := 5
	switch a {
	case "claude":
		timeout = 10 // its prompt hook may run the budget gate
	case "gemini":
		timeout = 5000 // Gemini counts milliseconds
	}
	for _, ev := range events(a) {
		var entry any
		if a == "cursor" {
			entry = map[string]any{"command": HookCommand(exe, a, ev)}
		} else {
			entry = map[string]any{"hooks": []any{map[string]any{"type": "command", "command": HookCommand(exe, a, ev), "timeout": timeout}}}
		}
		list, _ := hooks.get(ev).([]any)
		hooks.set(ev, append(list, entry))
	}
	if a == "cursor" && doc.get("version") == nil {
		doc.set("version", 1)
	}
	doc.set("hooks", hooks)
	return writeJSON(file, doc)
}

// Uninstall removes Signals' hooks from an agent's config, keeping everything
// else, and removes the agent's folder if Signals created it and it's unused.
func Uninstall(a string) error {
	file := ConfigFile(a)
	if a == "copilot" {
		os.Remove(file)
	} else if doc, err := readOrdered(file); err == nil && len(doc.keys) > 0 {
		if h := strip(doc.get("hooks"), a == "cursor"); len(h.keys) > 0 {
			doc.set("hooks", h)
		} else {
			doc.del("hooks")
		}
		if err := writeJSON(file, doc); err != nil {
			return err
		}
	}
	marker := filepath.Join(SignalsHome(), "created", a)
	if _, err := os.Stat(marker); err == nil {
		doc, _ := readOrdered(file)
		other := false
		for _, k := range doc.keys {
			other = other || (k != "version")
		}
		if !other {
			os.Remove(file)
			os.Remove(file + ".bak.signals")
			removeEmptyDirs(agentRoot(a))
		}
		os.Remove(marker)
	}
	return nil
}

// strip removes our hook commands from a hooks object.
func strip(v any, flat bool) orderedMap {
	in, _ := v.(orderedMap)
	out := orderedMap{}
	for _, ev := range in.keys {
		list, _ := in.vals[ev].([]any)
		var kept []any
		for _, e := range list {
			m, _ := e.(map[string]any)
			if flat {
				if c, _ := m["command"].(string); ours.MatchString(c) {
					continue
				}
				kept = append(kept, e)
				continue
			}
			inner, _ := m["hooks"].([]any)
			var keep []any
			for _, h := range inner {
				hm, _ := h.(map[string]any)
				if c, _ := hm["command"].(string); !ours.MatchString(c) {
					keep = append(keep, h)
				}
			}
			if len(keep) > 0 {
				m["hooks"] = keep
				kept = append(kept, m)
			}
		}
		if len(kept) > 0 {
			out.set(ev, kept)
		}
	}
	return out
}

func walkCommands(v any, f func(string)) {
	switch t := v.(type) {
	case orderedMap:
		for _, k := range t.keys {
			walkCommands(t.vals[k], f)
		}
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && k == "command" {
				f(s)
			}
			walkCommands(x, f)
		}
	case []any:
		for _, x := range t {
			walkCommands(x, f)
		}
	}
}

func dirExists(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		os.Remove(d) // only succeeds when empty
	}
}

// ── JSON that keeps the top-level key order of the user's files ───────────────

type orderedMap struct {
	keys []string
	vals map[string]any
}

func (o *orderedMap) get(k string) any {
	if o.vals == nil {
		return nil
	}
	return o.vals[k]
}
func (o *orderedMap) set(k string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}
func (o *orderedMap) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// readOrdered reads a JSON object, keeping the order of its top-level keys and
// of the hooks inside. A missing file is an empty object.
func readOrdered(file string) (orderedMap, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) || len(bytes.TrimSpace(data)) == 0 {
		return orderedMap{}, nil
	}
	if err != nil {
		return orderedMap{}, err
	}
	o, err := decodeOrdered(json.NewDecoder(bytes.NewReader(data)))
	if err != nil {
		return orderedMap{}, err
	}
	if h, ok := o.vals["hooks"].(map[string]any); ok {
		o.vals["hooks"] = orderedFromMap(h, data)
	}
	return o, nil
}

func decodeOrdered(dec *json.Decoder) (orderedMap, error) {
	o := orderedMap{}
	t, err := dec.Token()
	if err != nil {
		return o, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return o, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return o, err
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return o, err
		}
		o.set(kt.(string), v)
	}
	_, err = dec.Token()
	return o, err
}

// orderedFromMap orders a decoded object's keys as they appear in the source.
func orderedFromMap(m map[string]any, src []byte) orderedMap {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	pos := func(k string) int {
		kb, _ := json.Marshal(k)
		if i := bytes.Index(src, append(kb, ':')); i >= 0 {
			return i
		}
		return bytes.Index(src, kb)
	}
	sort.SliceStable(keys, func(i, j int) bool { return pos(keys[i]) < pos(keys[j]) })
	o := orderedMap{}
	for _, k := range keys {
		o.set(k, m[k])
	}
	return o
}

func decode(data []byte) any {
	var v any
	json.Unmarshal(data, &v)
	return v
}

// writeJSON writes indented JSON (2 spaces, like the agents' own files) atomically.
func writeJSON(file string, v any) error {
	raw, err := marshal(v)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
