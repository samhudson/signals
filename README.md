# Signals

See your AI agent sessions at a glance, and keep an eye on what they use, from the macOS menu bar.

```
● ● ●  ⛌ 42%
```

**Sessions.** One light per agent session: 🔴 working, 🟡 needs you, 🟢 done. Click for the list, with each session's title, agent and folder. Works with Claude Code, Codex, Cursor, Gemini CLI and GitHub Copilot.

**Usage** (optional). After the lights, a gate and a figure:

- **On a Claude plan** (Pro, Max, Team): how much of your 5-hour session and weekly limits you've used, and when they reset. Your plan is detected, nothing to set. A plan never shows money, except when you're on usage credits.
- **On an API key** (pay as you go): what each Claude Code session on this Mac costs, this month's total against an optional budget, and the pace for the month. Priced from your Claude Code logs at API prices, so no keys are needed.
- **Codex on a ChatGPT plan**: its 5-hour and weekly limits, alongside Claude's.

The gate changes colour as you get close (amber at 80%) and when you're over (red). With a budget on Claude spend, new Claude Code prompts wait once it's spent, until you take a one-hour pass or raise the budget.

## Install

```sh
brew install samhudson/tap/signals
signals install
```

`signals install` wires up the agents it finds, starts Signals in your menu bar, sets it to start at login, and opens the setup window. Restart running agent sessions so they pick up the hooks.

Requires macOS 13 or later and `jq` (installed with the formula).

## Using it

Everything in the window is also on the command line:

```sh
signals status                    # sessions, usage and the app
signals settings                  # open the window

signals usage                     # your usage now
signals usage plan                # your Claude plan's limits (detected from Claude Code)
signals usage estimate            # pay as you go: what each session on this Mac costs
signals usage off                 # session lights only
signals budget 50                 # monthly budget in USD
signals pass 1h                   # let prompts through while over budget

signals install cursor            # turn one agent on (uninstall to turn it off)
signals uninstall                 # remove everything (--purge also removes settings and keys)
```

## How it works

Each agent runs a small hook on its own events (prompt sent, tool used, waiting for permission, finished), which writes the session's state to `~/.signals/sessions`. The menu bar app watches that folder.

Usage only reads files on this Mac. On a Claude plan, that's the usage Claude Code already saves (and the exact figures it gives its terminal status line, if you turn that on with `signals install --statusline`); Signals never uses your Claude login. On an API key, session costs come from the token counts in your Claude Code logs, at API prices. Codex's limits come from its own session logs. Nothing is sent anywhere else.

## Uninstall

```sh
signals uninstall --purge
brew uninstall signals
```

---

Made by Sam Hudson. MIT licensed.

Signals is an independent app, not made by or affiliated with Anthropic, OpenAI, Cursor, Google or GitHub. Claude and Claude Code are trademarks of Anthropic.
