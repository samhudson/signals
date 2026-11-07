package engine

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Hook records an agent session's state from one hook event:
//
//	working  the prompt was sent, or tools are running
//	waiting  it needs you: a permission prompt or a question
//	done     the turn finished or was interrupted
//	error    the turn ended on an error
//
// The event comes as an argument because not every agent puts it in the
// payload. Gate, when set, runs the Usage budget gate before a Claude Code
// prompt; if it returns 2 the prompt is held and the session keeps its state.
type Hook struct {
	Agent, Event string
	Input        []byte
	Now          time.Time
	Stdout       io.Writer
	Stderr       io.Writer
	Gate         func(input []byte) (code int, message string)
}

// Run handles the event and returns the hook's exit code.
func (h Hook) Run() int {
	if h.Agent == "" {
		return 0
	}
	var p map[string]any
	_ = json.Unmarshal(h.Input, &p)
	event := h.Event
	if event == "" {
		event = field(p, "hook_event_name", "hookEventName")
	}
	cwd := field(p, "cwd")
	if cwd == "" {
		if roots, ok := p["workspace_roots"].([]any); ok && len(roots) > 0 {
			cwd, _ = roots[0].(string)
		}
	}
	sid := field(p, "session_id", "sessionId", "conversation_id", "conversationId")
	// Copilot doesn't always send a session id: one session per folder.
	if sid == "" && cwd != "" {
		sum := sha1.Sum([]byte(cwd))
		sid = "dir-" + hex.EncodeToString(sum[:])[:12]
	}
	if sid == "" {
		return 0
	}
	file := filepath.Join(SessionsDir(), h.Agent+"-"+sid+".json")
	finish := func() int {
		// Cursor's beforeSubmitPrompt expects a decision on stdout.
		if h.Agent == "cursor" && event == "beforeSubmitPrompt" && h.Stdout != nil {
			fmt.Fprintln(h.Stdout, `{"continue": true}`)
		}
		return 0
	}

	status, detail, end := classify(h.Agent, event, p)
	if end {
		os.Remove(file)
		return finish()
	}
	if status == "" {
		return finish()
	}

	// The budget gate: a held prompt never starts working.
	if h.Agent == "claude" && event == "UserPromptSubmit" && h.Gate != nil {
		if code, msg := h.Gate(h.Input); code == 2 {
			if h.Stderr != nil {
				fmt.Fprintln(h.Stderr, msg)
			}
			return 2
		}
	}

	state := map[string]any{}
	if old, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(old, &state)
		if state == nil {
			state = map[string]any{}
		}
	}
	state["agent"] = h.Agent
	state["session_id"] = sid
	state["status"] = status
	state["updated"] = h.Now.Unix()
	if cwd != "" {
		state["cwd"] = cwd
	}
	if t := field(p, "transcript_path"); t != "" {
		state["transcript"] = t
	}
	// Title fallback for agents that don't name sessions: the first line of the
	// session's first prompt. Claude Code's own title is read from the transcript.
	if old, _ := state["title"].(string); old == "" {
		if t := Clip(FirstLine(field(p, "prompt", "userPrompt"))); t != "" {
			state["title"] = t
		}
	}
	if detail != "" {
		state["detail"] = detail
	} else {
		delete(state, "detail")
	}
	if err := os.MkdirAll(SessionsDir(), 0o755); err != nil {
		return finish()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // titles keep their < > & as written
	if enc.Encode(state) != nil {
		return finish()
	}
	tmp := file + ".tmp"
	if os.WriteFile(tmp, buf.Bytes(), 0o644) == nil {
		os.Rename(tmp, file)
	}
	return finish()
}

// classify maps an agent's event to a state. end means the session is over.
func classify(agent, event string, p map[string]any) (status, detail string, end bool) {
	key := agent + "/" + event
	switch {
	case event == "SessionEnd" || event == "sessionEnd":
		return "", "", true
	case event == "SessionStart" || event == "sessionStart",
		key == "claude/Stop", key == "codex/Stop", key == "codex/Interrupt",
		key == "gemini/AfterAgent", key == "copilot/agentStop":
		return "done", "", false
	case key == "cursor/stop":
		if field(p, "status") == "error" {
			return "error", "error", false
		}
		return "done", "", false
	case key == "claude/StopFailure":
		d := field(p, "error_type")
		if d == "" {
			if e, ok := p["error"].(map[string]any); ok {
				d, _ = e["type"].(string)
			} else {
				d, _ = p["error"].(string)
			}
		}
		return "error", d, false
	case event == "UserPromptSubmit", event == "PostToolUse", event == "postToolUse",
		key == "gemini/BeforeAgent", key == "gemini/AfterTool",
		key == "cursor/beforeSubmitPrompt", key == "copilot/userPromptSubmitted":
		return "working", "", false
	case key == "claude/PermissionRequest", key == "codex/PermissionRequest":
		return "waiting", "", false
	case key == "claude/Notification":
		// Only notifications that mean "blocked on you"; idle reminders, auth
		// success, quota notices etc. are informational.
		switch field(p, "notification_type") {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			return "waiting", "", false
		}
		return "", "", false
	case key == "gemini/Notification":
		// Gemini's only notification type is ToolPermission.
		return "waiting", "", false
	}
	return "", "", false
}

// field returns the first of the keys that holds a non-empty string.
func field(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := p[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// Editors add context to a prompt (the VS Code extension wraps the selection
// or open file in <ide_…> tags); it isn't the title.
var contextBlock = regexp.MustCompile(`<(ide_[a-z_]+|system-reminder)>[\s\S]*?</(ide_[a-z_]+|system-reminder)>`)
var spaces = regexp.MustCompile(`\s+`)

// FirstLine is a prompt's first real line, without editor context.
func FirstLine(prompt string) string {
	for _, line := range strings.Split(contextBlock.ReplaceAllString(prompt, ""), "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// Clip collapses whitespace and shortens to 48 characters.
func Clip(s string) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	return clipRunes(s)
}

func clipRunes(s string) string {
	r := []rune(s)
	if len(r) > 48 {
		return string(r[:47]) + "…"
	}
	return s
}
