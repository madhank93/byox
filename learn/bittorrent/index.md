---
title: Build your own BitTorrent — primer
concepts: [bencode, hashing, peer protocol, pipelining]
---

## The mental model

BitTorrent replaces "download a file from a server" with "download pieces from
strangers and prove each one is correct". Trust comes entirely from hashing: the
torrent file contains a SHA-1 of every piece, so a peer can lie but cannot lie
undetected. Everything else — trackers, handshakes, message types — is
plumbing around that check.

## Bencode

The metainfo format is bencode, four types, no whitespace, no ambiguity:

```
i42e                       integer
4:spam                     string: length, colon, raw bytes
l4:spami42ee               list
d3:cow3:moo4:spam4:eggse   dictionary, keys sorted, always strings
```

Strings are byte strings, not UTF-8 — piece hashes live in one, 20 raw bytes
each, concatenated. Decoding into Go's `any` is fine; the part that matters is
that you can **re-encode** the `info` dictionary exactly, because:

**The info hash is the SHA-1 of the bencoded `info` dictionary.** Re-serialise
with keys in the original sorted order and byte-identical values, or every peer
rejects you. This is why a decoder that "helpfully" converts numbers or reorders
keys will pass stage 2 and fail stage 4.

## Talking to the tracker

A plain HTTP `GET` with query parameters: `info_hash` (20 raw bytes,
percent-encoded — not hex), `peer_id`, `port`, `left`, and
`compact=1`. The compact response packs peers as 6 bytes each: 4 bytes IPv4,
2 bytes big-endian port.

## The peer protocol

A 68-byte handshake: `\x13` + `"BitTorrent protocol"` + 8 reserved bytes +
info hash + peer id. The reply has the same shape; the last 20 bytes tell you
who you reached.

After that, length-prefixed messages: 4-byte length, 1-byte id, payload.
`bitfield` (5), `interested` (2), `unchoke` (1), `request` (6), `piece` (7).
The sequence is fixed — wait for `bitfield`, send `interested`, wait for
`unchoke`, then request.

## Pieces, blocks and pipelining

A piece is typically 256 KiB, but a `request` maxes out at a 16 KiB **block**,
so each piece is many requests. Sending them one at a time works and is slow;
sending them all and matching replies by `(index, begin)` is the intended
design. Once the piece is assembled, SHA-1 it and compare against the hash from
the metainfo before writing a byte to disk.

## Magnet links

The later stages start from a magnet URI, which has the info hash but *not* the
info dictionary — so you fetch the metadata from a peer using the extension
protocol (reserved bit, `extended` message id 20, a bencoded handshake naming
`ut_metadata`). Same primitives, one more layer of negotiation.

## The stage arc

Bencode decode → parse the torrent → info hash → piece hashes → tracker peers →
handshake → download a piece → download the file → magnet parsing → metadata
exchange → download from a magnet link.

## Going deeper

- [BitTorrent protocol specification (BEP 3)](https://www.bittorrent.org/beps/bep_0003.html)
- [BEP 9 — extension for peers to send metadata files](https://www.bittorrent.org/beps/bep_0009.html)
- [Unofficial spec wiki](https://wiki.theory.org/BitTorrentSpecification)
