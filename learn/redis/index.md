---
title: Build your own Redis — primer
concepts: [tcp, resp, concurrency, persistence, replication]
---

## The mental model

Redis is a single-threaded server around a hash map, speaking a text protocol
over TCP. Almost every stage is one of three things: *parse a request*, *mutate
or read the map*, *serialise a reply*. The hard stages are the ones where a
second party appears — a replica, a blocked client, a subscriber — and you need
state that outlives one request.

## RESP, the wire protocol

RESP2 is length-prefixed and line-oriented; every element ends `\r\n`. Five
type bytes carry everything:

| Byte | Type | Example |
|---|---|---|
| `+` | simple string | `+OK\r\n` |
| `-` | error | `-ERR unknown command\r\n` |
| `:` | integer | `:42\r\n` |
| `$` | bulk string | `$5\r\nhello\r\n`, null is `$-1\r\n` |
| `*` | array | `*2\r\n$4\r\nECHO\r\n$2\r\nhi\r\n` |

Clients always send commands as an **array of bulk strings**. So the parser you
write in stage 1 is the parser you use for the next hundred stages — build it
to read from a `*bufio.Reader` and return `[]string`, and never re-read a
connection's bytes twice.

Two rules save hours later: bulk strings are binary-safe (use the length, never
`strings.Split`), and command names are case-insensitive while keys are not.

## Where Go's model differs from Redis's

Real Redis is single-threaded with an event loop. In Go the natural shape is
one goroutine per connection — which means the keyspace is shared mutable state
and needs a `sync.Mutex` (or `sync.RWMutex`). Every stage that adds a data type
adds a way to race:

- **Expiry** is lazy in Redis — check the deadline on read, don't sweep.
- **Blocking reads** (`BLPOP`, `XREAD BLOCK`) must not hold the lock while
  waiting. Poll with a short sleep, or park on a condition/channel.
- **Pub/sub and replication** write to *other* connections, so a per-connection
  write mutex is not optional.

## The persistence formats

- **RDB** is a binary snapshot: a `REDIS0011` magic, then opcode-driven
  sections (`0xFE` db selector, `0xFB` resize, `0xFD`/`0xFC` expiry in
  seconds/milliseconds, `0x00` string value). Lengths use a 2-bit prefix
  encoding where `0b11` means "this is a special integer encoding", not a
  length. `encoding/binary` with `binary.LittleEndian` does the numeric work.
- **AOF** is a command log — the same RESP you already parse, replayed at
  startup, plus a manifest file. Writing it is easy; the trap is filtering out
  commands that must not be logged, and replaying it *through* your own
  dispatcher instead of a second code path.

## Replication

The handshake is a fixed conversation (`PING` → `REPLCONF listening-port` →
`REPLCONF capa psync2` → `PSYNC ? -1`), answered with `+FULLRESYNC <id> 0` and
a raw RDB payload that is *not* terminated by `\r\n`. After that, the master
streams every write to replicas verbatim, and offsets become the source of
truth: replicas count bytes they process, `REPLCONF GETACK *` asks for that
count, and `WAIT` blocks until enough replicas have acknowledged an offset.
Off-by-one offsets are the single most common failure here — count the bytes
you *consumed*, including the command that asked.

## The stage arc

Ping/echo → keyspace with expiry → RDB → replication → streams → transactions →
lists → pub/sub → sorted sets → geospatial → ACL. Each block adds a data type
plus one new coordination problem; the protocol layer stops changing after the
first few stages.

## Going deeper

- [RESP protocol spec](https://redis.io/docs/latest/develop/reference/protocol-spec/)
- [Redis replication](https://redis.io/docs/latest/operate/oss_and_stack/management/replication/)
- [RDB file format](https://rdb.fnordig.de/file_format.html)
- [Redis commands reference](https://redis.io/docs/latest/commands/)
