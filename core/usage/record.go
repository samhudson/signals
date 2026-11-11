// Package usage is Signals' usage ledger: it reads what each coding agent
// already writes on this computer (Claude Code transcripts, Codex session
// logs), keeps one numbers-only record per API call, prices it at list prices,
// and summarises it. Nothing here reads prompts, replies or code, and nothing
// leaves the computer.
package usage

import (
	"crypto/sha256"
	"encoding/hex"
)

// Call is one API call an agent made: numbers only. Token fields never
// overlap: In excludes cache reads and writes, Out includes reasoning (Rsn is
// the reasoning part, for information).
type Call struct {
	V    int    `json:"v"`
	K    string `json:"k"`              // dedupe key: hash of agent and the call's own id
	A    string `json:"a"`              // agent: claude, codex, gemini
	Via  string `json:"via,omitempty"`  // which app of the agent, when known (e.g. codex_vscode)
	T    int64  `json:"t"`              // unix seconds
	D    string `json:"d"`              // local day, YYYY-MM-DD
	S    string `json:"s,omitempty"`    // session id (stays on this computer)
	P    string `json:"p,omitempty"`    // working folder (stays on this computer)
	M    string `json:"m"`              // model
	Acct string `json:"acct,omitempty"` // hash of the account (vendor:id), never the id itself
	AK   string `json:"ak,omitempty"`   // account kind: individual, team, enterprise, api, unknown
	Plan string `json:"plan,omitempty"` // plan name when the agent records one (Codex)
	ASrc string `json:"asrc,omitempty"` // where Acct came from: login, session, backfill, none
	In   int64  `json:"in"`
	CR   int64  `json:"cr"`   // cache reads
	CW5  int64  `json:"cw5"`  // cache writes (5 minutes, or the vendor's only kind)
	CW1h int64  `json:"cw1h"` // cache writes (1 hour)
	Out  int64  `json:"out"`
	Rsn  int64  `json:"rsn,omitempty"`
	WS   int64  `json:"ws,omitempty"` // web searches
	Fast bool   `json:"fast,omitempty"`
	Sub  bool   `json:"sub,omitempty"` // made by a subagent
	line int    // where it was first seen in its log (for verify); not stored
}

// Prompt is everything the model read for this call: what long-prompt prices
// are measured on.
func (c Call) Prompt() int64 { return c.In + c.CR + c.CW5 + c.CW1h }

// Tokens is every token the call used.
func (c Call) Tokens() int64 { return c.Prompt() + c.Out }

// hash16 is the first 16 hex characters of sha256(parts joined by ':').
func hash16(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{':'})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Key is the dedupe key for a call: the same call read twice (a line written
// per content block, a resumed session, a re-read file) has the same key.
func Key(agent, id string) string { return hash16(agent, id) }

// AccountID hashes an account id so it can be compared (is this the company's
// account?) without ever being stored or sent.
func AccountID(vendor, id string) string {
	if id == "" {
		return ""
	}
	return hash16(vendor, id)
}
