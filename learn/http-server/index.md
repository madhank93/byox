---
title: Build your own HTTP server — primer
concepts: [tcp, http/1.1, framing, concurrency, compression]
---

## The mental model

HTTP/1.1 is a text protocol with one hard rule: **the receiver must know where
each message ends before it can read the next one.** Everything you implement —
the blank line after headers, `Content-Length`, connection reuse — exists to
answer that one question. `net/http` is off-limits here, and that is the point:
you write the framing yourself.

## The message shape

```
GET /echo/hello HTTP/1.1\r\n     <- request line: method, target, version
Host: localhost:4221\r\n         <- headers, one per line
User-Agent: curl/8.4.0\r\n
\r\n                             <- blank line ends the headers
<body bytes, exactly Content-Length of them>
```

Line terminator is CRLF (`\r\n`), not `\n`. Header names are
case-insensitive — normalise them once on read. The blank line is the frame
boundary; after it, you read *exactly* `Content-Length` bytes and not one more,
because the next byte belongs to the next request on the same connection.

A response is the same shape with a status line: `HTTP/1.1 200 OK\r\n`.
A 200 with no body still needs the blank line, and a body always needs a
matching `Content-Length`.

## Reading it in Go

`bufio.Reader` over the `net.Conn` gives you `ReadString('\n')` for the head
and `io.ReadFull` for the body. Use the *same* reader for both — wrapping the
conn twice loses whatever the first reader buffered, which is the classic
"my second request is garbage" bug.

Write through a single `bufio.Writer` (or build the response in a
`bytes.Buffer` and do one `Write`), so a response never interleaves with
another goroutine's.

## Concurrency

`l.Accept()` in a loop, `go handle(conn)` per connection. Nothing is shared
until the file-serving stages, where the directory is read-only and safe. The
subtlety is not races — it is lifetime: `defer conn.Close()` in the handler,
and don't close it between requests when the client wants keep-alive.

## Persistent connections and closing

HTTP/1.1 keeps connections open by default. Loop reading requests off one
connection until the client sends `Connection: close` — then echo
`Connection: close` back and close. Getting this wrong looks like a hang: the
client is politely waiting for a response you already finished sending, on a
connection you already forgot about.

## Compression

`Accept-Encoding: gzip` is a *list*, possibly with unknown schemes. Match gzip
anywhere in the list, ignore the rest, and only then set
`Content-Encoding: gzip`. `compress/gzip` writes the body — remember
`Content-Length` is the length of the **compressed** bytes, and the gzip writer
must be closed before you measure it.

## The stage arc

Bind a port → parse a request line → respond with status codes → echo path
segments → read a header → concurrency → serve and accept files → persistent
connections → gzip. Fourteen stages, and roughly half of them are framing.

## Going deeper

- [RFC 9112 — HTTP/1.1 message syntax](https://www.rfc-editor.org/rfc/rfc9112)
- [MDN: HTTP messages](https://developer.mozilla.org/en-US/docs/Web/HTTP/Messages)
- [`bufio` package docs](https://pkg.go.dev/bufio)
