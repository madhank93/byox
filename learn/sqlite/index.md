---
title: Build your own SQLite — primer
concepts: [file formats, b-trees, varints, record encoding, indexes]
---

## The mental model

A SQLite database is one file, and the file *is* the data structure: a series
of fixed-size pages, each page a node of a B-tree. You are writing a reader, not
a database — no writes, no transactions, no locking. Every stage is "find the
right page, decode it, and answer a question".

## The layout

- **Bytes 0–99: the file header.** The magic string `SQLite format 3\x00`, then
  at offset 16 the **page size** (2 bytes, big-endian — SQLite is big-endian
  throughout, unlike most on-disk formats). Everything else is derived from
  that page size.
- **Page 1** holds the header *and* the root of `sqlite_schema`, the table that
  lists every table and index with its original `CREATE` statement. You will
  parse SQL out of that column — a real, if small, parser.
- **Pages 2..N** are B-tree nodes, cell pointer arrays, and overflow.

Page numbers are 1-based, so page *n* starts at `(n-1) * pageSize`. Page 1 is
the exception where content begins at offset 100.

## B-tree pages

A page starts with an 8- or 12-byte header. The first byte is the type:

| Byte | Page |
|---|---|
| `0x0d` | table leaf — holds actual rows |
| `0x05` | table interior — holds child page numbers |
| `0x0a` | index leaf |
| `0x02` | index interior |

Interior pages have a 12-byte header because of the trailing right-most child
pointer; leaves have 8. After the header comes an array of 2-byte offsets, one
per cell, pointing into the page. Cells grow from the end of the page toward
the middle — so offsets are unordered and you must follow them, not walk
linearly.

Counting rows in a table is therefore "read the cell count from the page
header" for a leaf, or a full traversal once interior pages appear.

## Varints and the record format

Two encodings do all the work inside a cell:

- **Varint**: big-endian, 7 bits per byte, high bit means "continue", up to 9
  bytes. Used for row ids, payload sizes, and every field in the record header.
- **Record**: a header of serial-type varints followed by the values. The
  serial type says both the type and the size: `0` = NULL, `1`–`6` = integers of
  1,2,3,4,6,8 bytes, `7` = float, and for `N >= 12`, even means a blob of
  `(N-12)/2` bytes and odd means text of `(N-13)/2` bytes. That odd/even trick
  is the one piece of the format nobody guesses.

A row's `INTEGER PRIMARY KEY` column is stored as NULL in the record — the real
value is the cell's row id. Ignoring that returns a column of empty strings.

## Indexes

The final stages make a `WHERE` lookup use an index instead of a scan. An index
B-tree stores `(key, rowid)` records; you search it for the key, collect row
ids, then fetch exactly those rows from the table B-tree. Same decoder, different
tree — which is the payoff for keeping the record reader generic.

## The stage arc

Read the header → count tables → count rows → read a column → read multiple
columns → filter with `WHERE` → full table scan with a filter → use an index.

## Further reading

- [SQLite database file format](https://www.sqlite.org/fileformat.html)
- [Record format §2.1](https://www.sqlite.org/fileformat.html#record_format)
- [Varint encoding](https://www.sqlite.org/fileformat.html#varint)
