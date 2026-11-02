# Releasing

Releases follow [Semantic Versioning](https://semver.org) and are cut from `main` with one command. A tag starts the pipeline; nothing is published by hand.

```
push or pull request ──▶ CI: tests + app build on macOS
                          │
script/release --rc ──▶ vX.Y.Z-rc.N ──▶ GitHub pre-release            try it
                          │
script/release --promote ──▶ vX.Y.Z ──▶ GitHub Release (tarball + SHA-256)
                                         ├─▶ Homebrew tap points at that tarball
                                         └─▶ install check: brew install from the tap on a clean Mac
```

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org). They decide the next version and write the changelog.

| Prefix | Example | Next version |
|---|---|---|
| `fix:` | `fix: keep one copy of the app running` | patch (1.2.3 → 1.2.4) |
| `feat:` | `feat: show Codex limits` | minor (1.2.3 → 1.3.0) |
| `feat!:` or `BREAKING CHANGE:` | `feat!: new settings format` | major (1.2.3 → 2.0.0) |
| `docs:`, `test:`, `chore:`, `refactor:` | `docs: new screenshots` | listed under Other |

## Cut a release

```bash
script/release --dry-run          # see the next version and the changelog
script/release --rc               # tag a release candidate, e.g. v1.3.0-rc.1
script/release --promote          # the same commit becomes v1.3.0
```

Or skip the candidate: `script/release` (or `patch`, `minor`, `major`, `1.4.0`).

`script/release` checks that you're on a clean, pushed `main`, runs the tests, sets the version in the CLI, adds the changelog section, commits `chore(release): vX.Y.Z`, tags and pushes.

## What the pipeline does

**CI** (`.github/workflows/ci.yml`) runs the tests and builds the app on every push and pull request, on GitHub's macOS runners. It's read-only and gets no secrets, so pull requests from forks are safe.

**Release** (`.github/workflows/release.yml`) runs on version tags:

1. Checks the tag is a version and matches the code.
2. Runs the tests and builds the app, exactly as `brew install` will.
3. Publishes a GitHub Release with the source tarball, its SHA-256 and the changelog section. Release candidates are marked pre-release and never reach the tap.
4. For final releases, points the Homebrew formula at the release's tarball (`script/update-tap`).
5. Installs the new version from the tap on a clean Mac and runs `brew test`, the way users will. (Skipped while the tap is private.)

Run it by hand on any tag from the Actions tab (dry run by default) to check a release without publishing.

**Dependabot** opens a pull request when a pinned GitHub Action has an update. Actions are pinned to commit SHAs.

## Download counts

Homebrew downloads the tarball attached to the GitHub Release, and GitHub counts every request for it. So every `brew install` and `brew upgrade` is counted (Homebrew's own analytics only cover its official repositories, not personal taps). Homebrew checks the file's headers before downloading it, and GitHub counts that check too, so one install shows up as about 2 downloads; `script/downloads` shows both. Counts appear about 10 to 15 minutes after the download.

```bash
script/downloads          # installs per release, totals, and repo views/clones
script/downloads --json   # the same as JSON
```

The **Download stats** workflow (`.github/workflows/stats.yml`) saves a snapshot every Monday to the `stats` branch (`downloads.jsonl`), because GitHub keeps views and clones for only 14 days. The release's install check installs once per final release. Counting starts once the repo is public: a private repo's formula installs from the git tag, which isn't counted.

## One-time setup: the tap deploy key

The Release workflow updates `samhudson/homebrew-tap` with an SSH deploy key that can only write to the tap:

1. `ssh-keygen -t ed25519 -N "" -C "signals release" -f /tmp/signals-tap`
2. Add `/tmp/signals-tap.pub` to the tap: `gh repo deploy-key add /tmp/signals-tap.pub -R samhudson/homebrew-tap --allow-write -t "signals release"`
3. Save the private key in this repo: `gh secret set TAP_DEPLOY_KEY -R samhudson/signals < /tmp/signals-tap`
4. Delete both files: `rm /tmp/signals-tap /tmp/signals-tap.pub`

(A fine-grained token in `TAP_TOKEN`, limited to the tap's contents, works too.) Without either, releases still publish; the workflow warns and the tap can be updated by hand with `script/update-tap vX.Y.Z`.

## Going public

1. Make the tap public, so `brew install samhudson/tap/signals` works for everyone, then this repo.
2. Protect `main`: require the CI check before merging (Settings → Branches).
3. Turn on private vulnerability reporting (Settings → Security), which SECURITY.md points to.
4. Cut the release. The formula switches to the counted tarball on the first release after the repo is public.

## Where it's hosted

- **GitHub Releases**: every version's source tarball and checksum (the counted download).
- **Homebrew tap** (`samhudson/homebrew-tap`): how people install.
- The tarball and checksum are plain files, so they can be mirrored anywhere later without changing the formula's checksum.
