# Changelog

All notable changes, newest first. Versions follow [Semantic Versioning](https://semver.org).

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
