# gitling

[![CI](https://github.com/lcondliffe/gitling/actions/workflows/ci.yml/badge.svg)](https://github.com/lcondliffe/gitling/actions/workflows/ci.yml)

A terminal-native, at-a-glance summary of a git repository: recent activity,
top contributors, and codebase growth. Run it once at the start of a session to
orient yourself — it's not a replacement for `git log` or a full TUI.

<img src="docs/screenshot.png" alt="gitling dashboard: boxed panels in two columns — repo vitals across the top, an activity heatmap and hot files on the left, top contributors and codebase growth on the right, and recent commits along the bottom" width="900">

Six panels on one screen. They lay out in two columns on a terminal at least
100 columns wide and stack into one below that; `--layout wide|stack` forces
either shape.

## Install

```sh
brew install lcondliffe/tap/gitling
```

Or with Go:

```sh
go install github.com/lcondliffe/gitling/cmd/gitling@latest
```

That writes to `$GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset — make
sure it's on your `PATH`. Prebuilt binaries are on the
[latest release](https://github.com/lcondliffe/gitling/releases/latest).

## Usage

```
gitling                  # default dashboard (last 14 weeks)
gitling --since 30d      # override the range for all sections (d, w, mo, y)
gitling graph --since 1y # focused activity drill-down
gitling churn --since 1y # file churn: all files, ranked by commit count
gitling contributors     # all authors, ranked (--since sets the window)
gitling branches         # branch overview: ahead/behind, last commit, author
gitling tidy             # dry run: local branches that are safe to delete
gitling tidy --apply     # actually delete them (prompts once)
gitling --recent 10      # list the last 10 commits (0 hides the panel)
gitling --layout stack   # force one column; --layout wide forces two
gitling --json           # structured dashboard data for scripts/integrations
gitling --date commit    # bucket by commit date instead of author date
gitling --color=always   # always, never, or auto (default; honors NO_COLOR)
gitling --config ~/gitling.json  # use an explicit config file
```

Each drill-down is available as a subcommand or the matching `--flag`; naming
two different views is an error.

## Tidy

`gitling tidy` is the one subcommand that changes anything. It finds the local
branches you're done with and, on request, deletes them:

```
gitling tidy                  # dry run over merged + upstream-gone branches
gitling tidy --apply          # delete them, prompting once first
gitling tidy --merged         # narrow to branches merged into the default branch
gitling tidy --gone           # narrow to branches whose upstream was deleted
gitling tidy --stale          # also include branches untouched for 90 days
gitling tidy --stale 180d     # ...with a different threshold
gitling tidy --protect 'release/*'   # never delete matching branches
gitling tidy --apply --yes    # no prompt, for when you already know
gitling tidy --no-fetch       # skip the pruning fetch
```

```text
TIDY  ·  4 of 9 branches

  merged into origin/main   -d
    chore/tidy-readme   14d ago    a1b2c3d

  upstream gone (squash-merged)   -D
    feat/heatmap        1mo ago    9f8e7d6
    fix/parse-numstat   3mo ago    4c5b6a7

  4 branches to delete, 3 needing -D
  dry run — pass --apply to delete
```

### What it selects, and how much it trusts each category

Which group a branch lands in decides how it gets deleted:

- **merged** — the tip is an ancestor of the default branch, so the work is
  provably in. Deleted with `git branch -d`, leaving git's own merge check as a
  second safety net under gitling's.
- **gone** — the branch tracked a remote branch that no longer exists: the shape
  a squash-merged pull request leaves behind. The commits landed under new
  hashes, so git sees the branch as unmerged and only `-D` will drop it. The
  forge deleting the remote branch is evidence it's safe, but circumstantial
  rather than proof — which is why the plan marks these `-D` rather than hiding
  the distinction.
- **stale** — old, and neither merged nor gone. The only category where deleting
  can lose work, so it's never selected unless you ask with `--stale`.

`--merged` and `--gone` narrow the selection to what they name. `--stale` only
ever adds: asking to also clean up the old ones shouldn't quietly stop cleaning
up the safe ones.

"Merged" is measured against the *remote* default branch, so a branch merged
into your local `main` but never pushed is treated as unmerged and needs `-D`.

### Safety

- **Dry run by default.** Nothing is deleted without `--apply`, which prompts
  once (unless `--yes`) with the full plan on screen. Anything that isn't an
  explicit `y` — a bare newline, no stdin at all — is a no.
- **The current and default branches are never deleted**, nor anything matching
  a `--protect` glob or the config file's `protect` list. `--protect` adds to
  that list rather than replacing it.
- **Every branch shows the commit it pointed at**, before and after deletion, so
  anything can be restored with `git branch <name> <hash>`.
- **It fetches with `--prune` first** (skip with `--no-fetch`), because
  "upstream gone" is meaningless against stale remote-tracking refs. A failed
  fetch warns and continues — being offline shouldn't stop you tidying merged
  branches — but it says so, because the plan is then built on older
  information.
- A branch git refuses to delete is reported and the rest still run.

`gitling tidy` needs the shell-out backend; under `-tags gogit` it refuses, as
that build is read-only.

## Config file

gitling optionally reads defaults from
`$XDG_CONFIG_HOME/gitling/config.json`, falling back to
`~/.config/gitling/config.json`. Override with `--config <path>` or
`GITLING_CONFIG`. A missing file is fine; a malformed one is reported to
stderr.

```json
{
  "since": "30d",
  "color": "auto",
  "bucket": "week",
  "recent": 5,
  "layout": "auto",
  "protect": ["release/*", "wip/keep-me"]
}
```

Command-line flags override the config file, which overrides the built-in
defaults. `protect` is the exception: `--protect` adds to the configured list
rather than replacing it, since a config saying "never delete `release/*`"
shouldn't be switched off by naming one more pattern.

## Build

```sh
go build ./cmd/gitling
```

Pure Go standard library — no external dependencies.

### Optional go-git backend

`internal/gitdata` sits behind a small `Backend` interface, implemented by
default by shelling out to `git`. A pure-Go [go-git](https://github.com/go-git/go-git)
implementation is available behind a build tag:

```sh
go build -tags gogit ./cmd/gitling
```

This trades the dependency-free default for not needing `git` on `PATH`. It
isn't the default because shell-out is still faster on this project's
benchmarks for the commit-log walk that dominates runtime. `GITLING_BACKEND=shell`
forces shell-out in a tagged binary. The build is read-only, and known
divergences are documented on `gogitRepo` in `internal/gitdata/gogit.go`.

### Optional sqlite cache backend

The default cache is a zero-dependency gob file under `.git/gitling-cache/`.
For very large repos, a sqlite-backed store is available behind a build tag:

```sh
go build -tags sqlite ./cmd/gitling
```

It uses [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — pure Go
and cgo-free, so the tagged build still cross-compiles without a C toolchain.
It implements the same `cache.Backend` interface as the gob store, so gitling
behaves identically either way; only the on-disk format changes.
