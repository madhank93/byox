---
title: Build your own Kafka — primer
concepts: [binary protocol, schema versioning, varints, log segments]
---

## The mental model

Kafka's protocol is a versioned binary RPC over TCP, and its storage is an
append-only log on disk. The broker you build does two separable things: speak
the wire format correctly enough that a real client accepts your replies, and
read records back out of log segment files. Most stage failures are the former,
and almost all of those are one byte in the wrong place.

## Framing

Every request and response is preceded by a 4-byte big-endian length. Read that,
read exactly that many bytes, then parse — one message per frame, no ambiguity.

A request header carries `api_key`, `api_version`, `correlation_id`, and a
client id. The response echoes `correlation_id` first, which is how a client
matches replies to in-flight requests. Getting that echo right is stage one and
it never changes.

## Versioning is the protocol

Each API has its own schema *per version*. `api_version` is not decoration: it
decides which fields exist. Two consequences:

- An unsupported version is not a parse error, it is an error **code** (35,
  `UNSUPPORTED_VERSION`) in a correctly-framed response.
- `ApiVersions` is how a client learns what you support, so it must be
  answerable before anything else works.

## Flexible versions

Newer versions are "flexible", which changes the primitives underneath you:

- **Compact strings/arrays** carry length as an unsigned varint that is
  `actual_length + 1`, so `0` means null and `1` means empty.
- **Tagged fields**: a varint count of optional trailing fields, at the end of
  the header *and* of every struct. Almost always `0` — but the byte must be
  there, and omitting it shifts everything after it.

Signed integers in records use **zigzag** varints; lengths in the protocol do
not. Mixing those up is the other classic byte-offset bug.

## The metadata log

`DescribeTopicPartitions` and `Fetch` need to know which topics exist, and the
answer is not in memory — it is in `__cluster_metadata-0/…​.log`, on disk, in
the same record-batch format as any topic. So you parse:

**Record batch header** (base offset, batch length, CRC, magic, record count)
→ **records**, each a varint-length-prefixed struct with varint field sizes →
for the metadata log, **typed records** (topic, partition, feature-level)
identified by a type byte.

Reading a normal partition's log for `Fetch` uses that same batch parser, which
is why it is worth writing carefully once.

## The stage arc

Correlation id → error codes → `ApiVersions` → `DescribeTopicPartitions`
(including reading the metadata log) → `Fetch` from a partition → `Produce`.
Twenty-five stages, of which the parser you build in the first ten does most of
the work in the last fifteen.

## Further reading

- [Kafka protocol guide](https://kafka.apache.org/protocol.html)
- [Record batch format](https://kafka.apache.org/documentation/#recordbatch)
- [KIP-482 — flexible versions and tagged fields](https://cwiki.apache.org/confluence/display/KAFKA/KIP-482%3A+The+Kafka+Protocol+should+Support+Optional+Tagged+Fields)
