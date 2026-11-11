# Changelog

All notable changes, newest first. Versions follow [Semantic Versioning](https://semver.org).

## [1.2.0] - 2026-11-10

### Features

- Homebrew bottle with the engine, so brew install needs no compiler
- Codex in Usage on the Mac: the month by tool and Codex sessions
- signals usage summary, ledger and verify
- the ledger: per-call records by day, monthly totals, and incremental scans
- read per-call usage from Claude Code transcripts and Codex session logs
- list prices for Claude, OpenAI and Google models, priced in Go
- keep Claude plan usage current without /usage

### Fixes

- keep the Homebrew bottle location-independent
- build-bottle is executable
- learn the plan ratio only from readings with enough activity, as a weighted median; mark estimates with ≈
- say when a Claude usage reading predates a limit reset, instead of 'not started'
- on Linux without a desktop, say there's no tray instead of claiming it started

### Other

- Usage ledger and Homebrew bottle

## [1.1.0] - 2026-11-07

Signals now runs on Linux and Windows (beta): session lights in the system tray, for the same agents as on macOS.

### Features

- Linux and Windows downloads (Intel and ARM) on every release, with checksums in SHA256SUMS
- `signals install`, `uninstall`, `status`, `why` and `menu` on Linux and Windows; the tray starts at login
- On Windows, Signals adds itself to your PATH (and removes itself on uninstall)
- The download counter reports Homebrew installs, Linux and Windows downloads separately

### Notes

- Usage (plan limits and API spend) is macOS only for now
- The Windows programs aren't code-signed yet

## [1.0.1] - 2026-11-03

### Fixes

- catch Esc in Claude Code from the transcript, including on permission prompts; add signals why
- report raw downloads and estimated installs separately

### Other

- test: place past usage at local noon so a clock change can't merge two days
- docs: bring back the README graphics, redrawn for Signals

## [1.0.0] - 2026-11-01

First release of Signals: one menu bar app for your AI agent sessions and what they use.

### Features

- Sessions: one light per agent session for Claude Code, Codex, Cursor, Gemini CLI and GitHub Copilot: working, needs you, done
- Usage (optional): Claude plan limits as a percentage, or Anthropic or OpenAI API spend against a monthly budget
- A budget gate that holds new Claude Code prompts once the budget is spent, with a one-hour pass to keep going
- One menu bar item: the session lights, then the gate and your usage
- A setup window, and `signals` on the command line for everything it does
- Moves an existing trafficlight or tollbooth setup over on install
