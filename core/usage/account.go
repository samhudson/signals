package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samhudson/signals/core/engine"
)

// Where each agent keeps its files, honouring the agents' own overrides.
func claudeHome() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	return filepath.Join(engine.Home(), ".claude")
}

func claudeProjects() string { return filepath.Join(claudeHome(), "projects") }

func claudeAccountFile() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, ".claude.json")
	}
	return filepath.Join(engine.Home(), ".claude.json")
}

func codexSessions() string {
	h := os.Getenv("CODEX_HOME")
	if h == "" {
		h = filepath.Join(engine.Home(), ".codex")
	}
	return filepath.Join(h, "sessions")
}

// claudeLogin is the Claude account Claude Code is signed in with, from its
// own ~/.claude.json: the hashed organization and the kind of plan. Only the
// oauthAccount entry is read; never credentials.
func claudeLogin() (acct, kind string) {
	data, err := os.ReadFile(claudeAccountFile())
	if err != nil {
		return "", ""
	}
	var f struct {
		OAuthAccount *struct {
			OrganizationUUID string `json:"organizationUuid"`
			OrganizationType string `json:"organizationType"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &f) != nil || f.OAuthAccount == nil {
		return "", ""
	}
	return AccountID("anthropic", f.OAuthAccount.OrganizationUUID), claudeKind(f.OAuthAccount.OrganizationType)
}

// claudeKind sorts Claude's organization types into the kinds Teams filters on.
func claudeKind(t string) string {
	switch t = strings.ToLower(t); {
	case t == "":
		return "unknown"
	case strings.Contains(t, "enterprise"):
		return "enterprise"
	case strings.Contains(t, "team"):
		return "team"
	case strings.Contains(t, "pro"), strings.Contains(t, "max"), strings.Contains(t, "free"):
		return "individual"
	case strings.Contains(t, "api"), strings.Contains(t, "console"):
		return "api"
	}
	return "unknown"
}

// codexKind sorts ChatGPT plan types the same way.
func codexKind(plan string) string {
	switch p := strings.ToLower(plan); {
	case p == "":
		return "unknown"
	case strings.Contains(p, "enterprise"), p == "edu":
		return "enterprise"
	case strings.Contains(p, "business"), strings.Contains(p, "team"):
		return "team"
	case p == "free", p == "go", p == "plus", p == "pro":
		return "individual"
	case strings.Contains(p, "api"):
		return "api"
	}
	return "unknown"
}
