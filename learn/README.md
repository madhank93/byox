# `learn/` — byox's own teaching layer

CodeCrafters tells you *what to build*. This directory tells you *what you are
learning while you build it*, and it is the only stage content in the repo that
byox itself owns: the vendored `stage_descriptions/` are CodeCrafters' and are
never committed here.

```
learn/<course>/index.md      course primer — the domain background, read before stage 1
learn/<course>/NN-slug.md    stage note — the concept behind one stage
```

`NN-slug` matches the reference-solution directory names
(`reference-solutions/http-server/07-ap6/` ↔ `learn/http-server/07-ap6.md`),
where the slug is CodeCrafters' own stage id — so a stage's note, its snapshot
and its tester all key off the same identifier.

Both files are plain markdown with YAML frontmatter. Missing files are not an
error — byox falls back to a "not written yet" note, so coverage can grow one
stage at a time.

## Stage note shape

```markdown
---
title: Read a tree object
concepts: [binary parsing, zlib, sha-1]
---

## What this stage teaches

Two to four sentences on the idea, not the instructions. The reader already
has the task; what they lack is the model.

## Go you'll reach for

- `compress/zlib` — object files are zlib streams, not raw bytes.
- `bytes.IndexByte` — entries are NUL-delimited, so scan, don't split.

## Hints

<details><summary>Nudge</summary>...</details>
<details><summary>Approach</summary>...</details>
<details><summary>The API</summary>...</details>

## Going deeper

- [Git internals — tree objects](https://git-scm.com/book/en/v2/...)
```

## House rules

1. **Write it, don't quote it.** Never paste CodeCrafters stage prose; that
   content is paid and unredistributable. Explain the concept in your own
   words, from the specs and the source.
2. **Concept over instruction.** If a sentence would still be true with the
   tester deleted, it belongs here. If it starts "the tester will…", it does not.
3. **Hints ladder, never the answer.** Scale it to the stage: a hard stage
   gets all three steps — a nudge at the idea, an approach, then the specific
   API — while an easy one is often done after the nudge. Padding a one-line
   stage out to three hints just buries the useful one. The worked solution
   already lives in `reference-solutions/` behind a spoiler; don't duplicate
   it here.
4. **Link the primary source.** An RFC, a file-format spec, the Go package
   docs. Prefer the thing itself over a blog post about the thing.
