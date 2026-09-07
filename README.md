# build-your-own-x — local CodeCrafters runner

Complete [CodeCrafters](https://codecrafters.io) "Build your own X" courses **entirely locally**, in Go.
A Bubble Tea TUI + engine (`byox`) drives the **official open-source course testers** — the exact same
checks the real platform runs — with stage-by-stage progress tracking.

Courses wired up: **Redis** (115 stages), **Interpreter**, **Git** (7 stages), **SQLite**,
**DNS server** (8 stages), **HTTP server** (14 stages), **BitTorrent**, **grep**, **Shell**
(76 stages), and **Kafka**. Adding more is one entry in `courses.yml`.

> **Not affiliated with or endorsed by CodeCrafters, Inc.** This is a personal learning
> project. It contains original solutions to publicly available CodeCrafters challenges and
> uses their public, MIT-licensed starter repos. Course testers and paid stage-instruction
> content are **not** redistributed here — they are cloned locally at build time. See
> [`LICENSE`](./LICENSE) and [`THIRD_PARTY_NOTICES.md`](./THIRD_PARTY_NOTICES.md).

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
| `n` | show / hide the learning notes for the stage (or the course primer on a header row) |
| `/` | fuzzy-filter stages by name or slug, across all courses |
| `c` | jump to the current stage of the cursor's course |
| `J/K` / `pgup/pgdn` | scroll the instructions / log / solution pane |
| `esc` | log or solution → instructions, or clear filter |
| `q` | quit |

### Learning notes

CodeCrafters' stage instructions tell you *what to build*. `learn/` is byox's
own layer telling you *what you're learning while you build it* — and it is the
only stage content this repo owns, written from the specs rather than copied
from anywhere.

```
learn/<course>/index.md      course primer — the protocol or file format itself,
                             the Go packages that carry it, the traps
learn/<course>/NN-slug.md    per-stage note — the core concept, the Go APIs,
                             a three-step hint ladder, primary sources
```

Press `n` in the TUI: a stage row shows that stage's note, a course header row
shows the primer. Hints stay hidden until you press `f` — the terminal can't
collapse them the way the website's `<details>` do, and a hint you didn't ask
for isn't a hint. On the website the primers are pages under **Course primers**,
and a stage's note appears in its catalog modal between the instructions and the
reference-solution spoiler.

Coverage is partial by design and grows one stage at a time — `just gen` prints
it (`learn coverage: 10/10 primers, 14/390 stage notes`), and a stage with no
note yet says so instead of erroring. `learn/README.md` has the format and the
house rules for writing more.

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
- **sqlite**: all 9 stages ✓ (reading the file header/schema, counting
  rows, listing tables, reading single/multiple columns, filtering with
  `WHERE`, and using an index for a `WHERE` lookup)
- **grep**: all 33 stages ✓ (a backtracking regex engine — literals,
  character classes, anchors, quantifiers, alternation, backreferences,
  and grouping)
- **shell**: all 76 stages ✓ (a POSIX-ish interactive shell — quoting,
  redirection, raw-mode Tab completion and programmable completion,
  background jobs and pipelines, `history` with file persistence, and
  shell variables)
- **interpreter**: all 84 stages ✓ (a tree-walk Lox interpreter — scanner,
  recursive-descent parser, evaluator, a static Resolver pass for correct
  closure scoping, classes, and single inheritance with `super`)
- **kafka**: all 25 stages ✓ (a hand-rolled Kafka broker — wire-protocol
  request/response framing, ApiVersions, DescribeTopicPartitions, Fetch, and
  Produce, including parsing the `__cluster_metadata` log's record-batch
  format and reading/writing partition log segments on disk)
- **bittorrent**: all 19 stages ✓ (bencode, torrent parsing and info
  hashing, tracker announce, the peer wire protocol with pipelined block
  requests and pieces spread over every peer, plus magnet links — the BEP 10
  extension handshake and BEP 9 metadata exchange)

Each `reference-solutions/<course>/NN-slug/main.go` was verified by running the
official CodeCrafters tester cumulatively (stages 1..N) against it. The
authoring sources live in `reference-solutions/<course>-work/`, and
`reference-solutions/verify.sh` / `snapshot.sh` (or the `-local.sh` variants, which
default the working directory to this checkout's own `<course>-work`) reproduce the
verification, reading the stage lists in `reference-solutions/stages/`.
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
learn/<course>/     byox's own primers and per-stage concept notes
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
