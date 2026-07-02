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
  and dark backgrounds.

The layers are cleanly separated: the git backend (currently shell-out, a
go-git backend could replace it) and the cache (gob, could become sqlite) are
each swappable without touching the others.

## Build

```
go build ./cmd/gitling
```

Pure Go standard library — no external dependencies.

## Releases

Tagging a commit `vX.Y.Z` triggers the release workflow, which cross-compiles
binaries (linux/darwin/windows, amd64/arm64), attaches them with a
`checksums.txt`, and publishes a GitHub Release with auto-generated notes:

```
git tag v0.1.0
git push origin v0.1.0
```

## Status

v0.1. Drill-down views are starting to land: `gitling graph` / `--graph`
shows a focused activity view with day/week/month buckets. Other drill-downs
(`--churn`, `--contributors`, `--branches`) are reserved but not yet
implemented.
