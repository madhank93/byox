---
title: Getting started
description: Install byox and start working through CodeCrafters courses locally.
---

## Prerequisites

byox uses [mise](https://mise.jdx.dev/) to pin the toolchain (Go 1.25) and
`just`, so you don't need Go installed globally.

```sh
brew install mise        # macOS; see mise docs for other platforms
```

## Set up

```sh
git clone https://github.com/madhank93/build-your-own-x
cd build-your-own-x
mise install              # provisions Go 1.25 + just
just setup                # clones course + tester repos, builds testers, seeds Go starters
```

## Daily loop

```sh
just tui                  # single-screen TUI: stage list left, instructions right
```

Rustlings-style **watch mode** is always on: save any file in
`solutions/<course>/` and the current stage's tests run automatically,
streaming into the right pane.

Or headless:

```sh
just test redis           # run official tests for redis's current stage (+ all prior as regression)
just status                # progress across courses
```

Your code lives in `solutions/<course>/app/` — edit `main.go`, run tests,
pass, next stage unlocks. `progress.json` records completion. Passing
reruns every earlier stage too, exactly like the real platform.

## How it works

- `just setup` shallow-clones `codecrafters-io/build-your-own-<course>`
  (course definition + stage instructions + Go starter) and
  `codecrafters-io/<course>-tester` (official tester), then builds each
  tester to `testers/<name>/dist/main.out`.
- `byox` invokes the tester with `CODECRAFTERS_REPOSITORY_DIR=solutions/<course>`
  and `CODECRAFTERS_TEST_CASES_JSON` covering stages 1..current; exit 0
  marks the stage complete.
- Stage instructions render in the TUI from the vendored `stage_descriptions/*.md`.

### TUI keys

| Key | Action |
| --- | --- |
| `↑/↓` `j/k`, `g/G` | move / top / bottom (moves across all courses) |
| `enter` (on a course header) | fold / unfold that course |
| `esc esc` | fold / unfold the cursor's course from anywhere |
| `t` | run tests for the cursor's course (or just save a file) |
| `e` | open the solution's `main.go` in `$VISUAL`/`$EDITOR`; tests rerun on return |
| `s` | show / hide the reference solution for the selected stage |
| `/` | fuzzy-filter stages by name or slug, across all courses |
| `c` | jump to the current stage of the cursor's course |
| `J/K` / `pgup/pgdn` | scroll the instructions / log / solution pane |
| `esc` | log or solution → instructions, or clear filter |
| `q` | quit |

## Reference solutions

Pressing `s` in the TUI shows a reference solution for the selected stage.
This repo ships **complete, tester-verified Go solutions** for most stages,
authored under `reference-solutions/`. Each
`reference-solutions/<course>/NN-slug/main.go` was verified by running the
official CodeCrafters tester cumulatively (stages 1..N) against it before
being snapshotted. `byox` reads these first, falling back to CodeCrafters'
vendored free-stage solutions.

Browse every stage of every course — filter by course, search, and read the
worked solution — in the [Catalog](/catalog/).

## Other commands

```sh
just build                       # build the engine binary
just reset <course> <stage>      # rewind a course's progress pointer to a stage (code untouched)
```

Next: browse the [Catalog](/catalog/) to see every course and stage.
