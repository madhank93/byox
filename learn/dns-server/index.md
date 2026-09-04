---
title: Build your own DNS server — primer
concepts: [udp, binary protocol, bit fields, name compression]
---

## The mental model

DNS is a binary protocol over UDP where the whole message must fit in one
datagram, and that constraint explains every odd thing about its format:
packed bit fields instead of readable flags, length-prefixed labels instead of
dotted strings, and pointers so a repeated domain name is stored once.

Unlike TCP work, there is no stream and no framing problem: one `ReadFromUDP`
gives you exactly one complete message. The work is purely encode/decode.

## The packet

Every message — query and response alike — has the same five parts: a 12-byte
header, then question, answer, authority and additional sections.

The header is six 16-bit big-endian fields:

```
ID       16 bits   copied unchanged into the response
flags    16 bits   QR(1) OPCODE(4) AA(1) TC(1) RD(1) RA(1) Z(3) RCODE(4)
QDCOUNT  16 bits   questions that follow
ANCOUNT  16 bits   answer records
NSCOUNT / ARCOUNT
```

That flags word is where bit manipulation earns its keep:
`flags := qr<<15 | opcode<<11 | aa<<10 | tc<<9 | rd<<8 | ra<<7 | rcode`, and
reading it back is shifts and masks. `binary.BigEndian.PutUint16` handles the
byte order — DNS is network order, always.

## Names on the wire

`codecrafters.io` is encoded as length-prefixed labels, NUL-terminated:

```
\x0ccodecrafters\x02io\x00
```

There is no dot in the encoding. A label length is one byte, so it maxes at 63 —
and that is not an accident: the top two bits being `11` (`0xC0`) marks a
**compression pointer** instead of a length. The remaining 14 bits are an offset
from the start of the *message*, and the name continues there.

Two consequences worth internalising before you write the parser:

1. Parsing a name needs the whole message buffer, not just the current slice.
2. A pointer can point to a name that itself ends in a pointer, so parsing is
   recursive — and a malicious packet can make it loop. Bound the jumps.

## Records

An answer record is `NAME` (same encoding), then `TYPE`, `CLASS` (both 16-bit —
`1`/`1` for A/IN), `TTL` (32-bit), `RDLENGTH` (16-bit) and that many bytes of
data. For an A record the data is 4 raw bytes of IPv4 — not the text `1.2.3.4`.

## Forwarding

The last stages turn your server into a resolver: take the questions you were
asked, send them upstream, and merge the answers. The catch is that upstream
resolvers may refuse multi-question packets, so you split an N-question query
into N single-question queries and reassemble — which is the first time your
encoder and decoder are used *against each other* rather than against the
tester. Bugs that were symmetric until now suddenly show.

## The stage arc

UDP listener → write a header → write a question → write an answer → parse a
header → parse questions → resolve compressed names → forward to an upstream.

## Going deeper

- [RFC 1035 §4 — message format](https://www.rfc-editor.org/rfc/rfc1035#section-4)
- [RFC 1035 §4.1.4 — message compression](https://www.rfc-editor.org/rfc/rfc1035#section-4.1.4)
- [`encoding/binary` package docs](https://pkg.go.dev/encoding/binary)
