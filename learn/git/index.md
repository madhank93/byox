---
title: Build your own Git — primer
concepts: [content-addressed storage, zlib, sha-1, packfiles, delta encoding]
---

## The mental model

Git is a content-addressed key–value store with a thin layer of conventions on
top. Every object is stored under the SHA-1 of its own contents, so the hash is
both the name and the integrity check. Once that clicks, "commit", "branch" and
"clone" stop being magic: they are objects and pointers to objects.

## The four object types

An object on disk is `<type> <size>\x00<content>`, zlib-compressed, written to
`.git/objects/ab/cdef…` where the path is the first two and remaining hex digits
of the SHA-1 **of the uncompressed bytes including that header**. Hash first,
compress second — reversing those is the number-one stage-2 bug.

| Type | Content |
|---|---|
| `blob` | raw file bytes |
| `tree` | directory listing: repeated `<mode> <name>\x00<20-byte raw SHA>` |
| `commit` | text: `tree`, `parent`, `author`, `committer`, blank line, message |
| `tag` | annotated tags (not needed for these stages) |

Note the tree entry format: mode is ASCII (`100644`, `40000` — no leading zero
for directories), the name is NUL-terminated, and the hash is **20 raw bytes**,
not 40 hex chars. Entries are sorted by name. Because trees reference trees, a
whole directory snapshot is one hash.

## Go tools for the job

- `compress/zlib` — `NewReader` / `NewWriter` for every object read and write.
- `crypto/sha1` — `sha1.Sum` over the header + content.
- `bytes.IndexByte` — parse NUL-delimited fields; never `strings.Split` binary.
- `encoding/hex` — only at the boundary where humans see hashes.

## Cloning: the Smart HTTP protocol

The clone stage is a step up. Two requests:

1. `GET /info/refs?service=git-upload-pack` — a **pkt-line** stream. Each chunk
   is prefixed by 4 hex digits giving its total length; `0000` is a flush
   packet. Parse it as framing, not as text.
2. `POST /git-upload-pack` — you send the ref you `want`, the server replies
   with a packfile.

## Packfiles and deltas

A packfile is `PACK`, version, object count, then objects compressed
back-to-back. Each object has a variable-length header where the low bits of
the first byte carry a 3-bit type and the size arrives in 7-bit groups,
little-endian, with the high bit as "more follows". Two of those types are not
objects at all but **deltas** (`ofs-delta`, `ref-delta`): instructions to
rebuild an object from another one, as a series of copy-from-base and
insert-literal commands.

So resolution is two-phase: read every object, then repeatedly apply deltas
whose base you already have. A delta's base may itself be a delta, which is why
a single pass fails on real repositories.

## The stage arc

`init` → `cat-file` → `hash-object` → `ls-tree` → `write-tree` →
`commit-tree` → `clone`. The first six are one file format each; the last is
half the difficulty of the course.

## Further reading

- [Pro Git — Git internals: objects](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects)
- [Git docs — pack format](https://git-scm.com/docs/pack-format)
- [Git docs — protocol v2 / pkt-line](https://git-scm.com/docs/protocol-common)
