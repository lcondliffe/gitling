# gitling

[![CI](https://github.com/lcondliffe/gitling/actions/workflows/ci.yml/badge.svg)](https://github.com/lcondliffe/gitling/actions/workflows/ci.yml)

A terminal-native, at-a-glance summary of a git repository: recent activity,
top contributors, and codebase growth. Run it once at the start of a session to
orient yourself — it's not a replacement for `git log` or a full TUI.

<img src="docs/screenshot.png" alt="gitling dashboard showing repo vitals, an activity heatmap, top contributors, and codebase growth" width="520">

## Install

With Homebrew:

```sh
brew install lcondliffe/tap/gitling
```

Or with Go:

```
go install github.com/lcondliffe/gitling/cmd/gitling@latest
```

`go install` writes the binary to `$GOBIN`, or to `$(go env GOPATH)/bin` when
`GOBIN` is unset. Make sure that directory is on your `PATH`:

```
export PATH="$(go env GOPATH)/bin:$PATH"
```

For zsh, add that line to `~/.zshrc` so `gitling` is available in new
terminals too.

Or grab a prebuilt binary for your platform from the
[latest release](https://github.com/lcondliffe/gitling/releases/latest) and put
it on your `PATH`.

## Output

Four panels, single screen:

1. **Repo vitals** — branch, ahead/behind upstream, dirty files, stashes, branches.
2. **Activity heatmap** — GitHub-style contribution grid (default last 14 weeks),
   5-step intensity, today's cell marked with a hollow square. Total commits and
   current streak below.
3. **Top contributors** — up to 5 authors by commit count in range, with bars.
4. **Codebase growth** — total LOC, 6-month percent change, a trend sparkline,
   and the hottest files by churn.

## Usage

```
gitling                  # default dashboard (last 14 weeks)
gitling --since 30d      # override the range for all sections (d, w, mo, y)
gitling graph --since 1y # focused activity drill-down
gitling --graph --bucket week --since 1y
gitling churn --since 1y # file churn: all files, ranked by commit count
gitling contributors     # all authors, ranked (--since sets the window)
gitling branches         # branch overview: ahead/behind, last commit, author
gitling --json           # structured dashboard data for scripts/integrations
gitling --no-color       # plain output, no ANSI escape codes
```

Color is also auto-disabled when stdout isn't a terminal or `NO_COLOR` is set.

## How it works

- **gitdata** shells out to `git log --numstat` and a handful of cheap
  plumbing commands. Author date is used for bucketing.
- **aggregate** rolls commits up into per-day buckets (counts, line deltas,
  per-author and per-file tallies). Range queries sum the days in range, so
  changing `--since` never invalidates the cache.
- **cache** persists the rollup as a gob file under `.git/gitling-cache/`,
  keyed by the last HEAD seen. Each run only walks commits newer than the last,
  making repeat runs effectively instant.
- **render** draws everything with 256-color ANSI chosen to read on both light
  and dark backgrounds, or emits the same model as indented JSON when `--json`
  is set.

The layers are cleanly separated: the git backend (shell-out by default, with
an opt-in pure-Go go-git backend — see below) and the cache (gob, could
become sqlite) are each swappable without touching the others.

## Build

```
go build ./cmd/gitling
```

Pure Go standard library — no external dependencies.

### Optional go-git backend

The git interaction layer (`internal/gitdata`) sits behind a small `Backend`
interface. By default it's implemented by shelling out to the `git` binary,
which keeps the default build dependency-free. An alternative pure-Go
implementation using [go-git](https://github.com/go-git/go-git) is available
behind the `gogit` build tag:

```
go build -tags gogit ./cmd/gitling
```

This trades the dependency-free default for not needing `git` on `PATH`.
It's opt-in and not the default because, on this project's benchmarks,
shell-out is still faster for the commit-log walk that dominates gitling's
runtime; see `internal/gitdata/bench_test.go` /
`internal/gitdata/bench_gogit_test.go`. A `GITLING_BACKEND=shell` environment
variable can force shell-out even in a `gogit`-tagged binary; it has no
effect on the default build.

Known divergences from shell-out are documented on `gogitRepo` in
`internal/gitdata/gogit.go` (notably: author identity is not
mailmap-resolved, and stash count is always reported as 0 since go-git has
no porcelain equivalent of `git stash list`).

## Releases

Tagging a commit `vX.Y.Z` triggers the release workflow, which cross-compiles
binaries (linux/darwin/windows, amd64/arm64), attaches them with a
`checksums.txt`, and publishes a GitHub Release with auto-generated notes:

```
git tag v0.1.0
git push origin v0.1.0
```

## Status

v0.2. The drill-down subcommands have landed — each available as a
subcommand or the matching `--flag` (naming two different views errors):

- `graph` — focused activity view with day/week/month buckets.
- `churn` — every file touched in range, ranked by commit count.
- `contributors` — all authors ranked (beyond the dashboard's top 5).
- `branches` — per-branch ahead/behind vs upstream (or the default branch),
  last-commit age, and tip author.
