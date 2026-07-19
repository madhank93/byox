# build-your-own-x — local CodeCrafters runner

Complete [CodeCrafters](https://codecrafters.io) "Build your own X" courses **entirely locally**, in Go.
A Bubble Tea TUI + engine (`byox`) drives the **official open-source course testers** — the exact same
checks the real platform runs — with stage-by-stage progress tracking.

Courses wired up: **Redis** (115 stages), **HTTP server** (14 stages), **Git** (7 stages), and **DNS server** (8 stages).
Adding more is one entry in `courses.yml`.

## Prerequisites

[mise](https://mise.jdx.dev) (pins Go + just) — or open the repo in the included **devcontainer**,
which installs everything and runs setup automatically.

```sh
mise install     # installs go + just
just setup       # clones course + tester repos, builds testers, seeds Go starters
```

## Daily loop

```sh
just tui         # single-screen TUI: stage list left, instructions right
```

Rustlings-style **watch mode** is always on: save any file in `solutions/<course>/`
and the current stage's tests run automatically, streaming into the right pane.

or headless:

```sh
just test redis  # run official tests for redis's current stage (+ all prior as regression)
just status      # progress across courses
```

Your code lives in `solutions/<course>/app/` — edit `main.go`, run tests, pass, next stage
unlocks. `progress.json` records completion. Passing reruns every earlier stage too, exactly
like the real platform.

The layout is one grouped list: every course is a collapsible section with its
own inline progress; the top bar shows the course under the cursor with a
full-width progress bar. The right pane shows stage instructions (or the course
overview when a section header is selected), and becomes the live test log
during a run. Tests, watch mode, and the top bar all follow whichever course
the cursor is in.

### TUI keys

| Key | Action |
|-----|--------|
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

### Reference solutions

Pressing `s` shows a reference solution for the selected stage. This repo ships
**complete, tester-verified Go solutions for every stage** of these courses,
authored in-repo under `reference-solutions/`:

- **http-server**: all 14 stages ✓
- **redis**: all 115 stages ✓ (full command set — RDB, AOF, replication,
  streams, transactions, lists, pub/sub, sorted sets, geospatial, ACL/AUTH)
- **git**: all 7 stages ✓ (plumbing commands — init, cat-file, hash-object,
  ls-tree, write-tree, commit-tree — plus cloning a real GitHub repo over the
  Smart HTTP protocol, including packfile parsing and delta resolution)
- **dns-server**: all 8 stages ✓ (UDP server, DNS header/question/answer
  encoding and parsing, RFC 1035 name-compression pointer resolution,
  multi-question packets, and a forwarding resolver)
- **kafka**: all 25 stages ✓ (a hand-rolled Kafka broker — wire-protocol
  request/response framing, ApiVersions, DescribeTopicPartitions, Fetch, and
  Produce, including parsing the `__cluster_metadata` log's record-batch
  format and reading/writing partition log segments on disk)

Each `reference-solutions/<course>/NN-slug/main.go` was verified by running the
official CodeCrafters tester cumulatively (stages 1..N) against it. The
authoring sources live in `reference-solutions/{redis,http-server,git,dns-server}-work/`, and
`reference-solutions/verify.sh` / `snapshot.sh` (or the `-local.sh` variants, which use
this checkout's own paths instead of a hardcoded author path) reproduce the verification.
`byox` reads these first, falling back to CodeCrafters' vendored free-stage
solutions.

Stage rows: `✓` done · `▶` current · `○` locked, with stage number, slug
(for `byox reset`), and color-coded difficulty.

## How it works

- `just setup` shallow-clones `codecrafters-io/build-your-own-<course>` (course definition +
  stage instructions + Go starter) and `codecrafters-io/<course>-tester` (official tester),
  then builds each tester to `testers/<name>/dist/main.out`.
- `byox` invokes the tester with `CODECRAFTERS_REPOSITORY_DIR=solutions/<course>` and
  `CODECRAFTERS_TEST_CASES_JSON` covering stages 1..current; exit 0 marks the stage complete.
- Stage instructions render in the TUI from the vendored `stage_descriptions/*.md`.

## Layout

```
courses.yml        course registry (add new courses here)
engine/            Go module: byox CLI + TUI
solutions/<c>/     your code (seeded once from official Go starter, never overwritten)
vendor/            cloned course repos      (gitignored)
testers/           cloned + built testers   (gitignored)
progress.json      stage completion state   (gitignored)
```

## Commands

```sh
byox                          # TUI
byox setup                    # idempotent setup
byox test <course>            # test current stage
byox status                   # progress
byox reset <course> --stage <slug>   # rewind progress pointer (code untouched)
```

## Adding a course

Add an entry to `courses.yml` (course repo + tester repo from
[github.com/codecrafters-io](https://github.com/orgs/codecrafters-io/repositories)), then `just setup`.
