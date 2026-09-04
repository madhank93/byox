package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var fileDir string

func main() {
	flag.StringVar(&fileDir, "directory", "", "directory to serve files from")
	flag.Parse()

	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("Failed to bind to port 4221")
		os.Exit(1)
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	// HTTP/1.1 keeps the connection open by default, so serving is a loop.
	// The reader is created once, outside it: a second reader would discard
	// whatever the first had buffered, which is the next request.
	r := bufio.NewReader(conn)
	for {
		if !serveRequest(conn, r) {
			return
		}
	}
}

// serveRequest handles one request, reporting whether the connection should
// stay open for another.
func serveRequest(conn net.Conn, r *bufio.Reader) bool {
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	parts := strings.Fields(line)
	method, path := "GET", "/"
	if len(parts) >= 2 {
		method, path = parts[0], parts[1]
	}
	headers := readHeaders(r)
	// The request carrying Connection: close still gets a full response; it
	// is the connection that ends, not the exchange.
	keepAlive := !strings.EqualFold(headers["connection"], "close")

	var body []byte
	if cl := headers["content-length"]; cl != "" {
		n, _ := strconv.Atoi(cl)
		body = make([]byte, n)
		io.ReadFull(r, body)
	}

	switch {
	case path == "/":
		writeStatus(conn, "200 OK", keepAlive)
	case strings.HasPrefix(path, "/echo/"):
		writeText(conn, strings.TrimPrefix(path, "/echo/"), headers["accept-encoding"], keepAlive)
	case path == "/user-agent":
		writeText(conn, headers["user-agent"], "", keepAlive)
	case strings.HasPrefix(path, "/files/"):
		name := strings.TrimPrefix(path, "/files/")
		if method == "POST" {
			os.WriteFile(filepath.Join(fileDir, name), body, 0o644)
			writeStatus(conn, "201 Created", keepAlive)
		} else {
			serveFile(conn, name, keepAlive)
		}
	default:
		writeStatus(conn, "404 Not Found", keepAlive)
	}
	return keepAlive
}

// writeStatus writes a bodiless response, echoing Connection: close when the
// client asked to end the connection.
func writeStatus(conn net.Conn, status string, keepAlive bool) {
	closing := ""
	if !keepAlive {
		closing = "Connection: close\r\n"
	}
	fmt.Fprintf(conn, "HTTP/1.1 %s\r\n%s\r\n", status, closing)
}

func serveFile(conn net.Conn, name string, keepAlive bool) {
	data, err := os.ReadFile(filepath.Join(fileDir, name))
	if err != nil {
		writeStatus(conn, "404 Not Found", keepAlive)
		return
	}
	closing := ""
	if !keepAlive {
		closing = "Connection: close\r\n"
	}
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\n%sContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n", closing, len(data))
	conn.Write(data)
}

func readHeaders(r *bufio.Reader) map[string]string {
	headers := map[string]string{}
	for {
		h, err := r.ReadString('\n')
		if err != nil {
			break
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" {
			break
		}
		k, v, ok := strings.Cut(h, ":")
		if ok {
			headers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return headers
}

// acceptsGzip reports whether gzip appears in an Accept-Encoding list. Most
// HTTP headers are lists, and substring matching on the raw value would accept
// "x-gzip-2".
func acceptsGzip(accept string) bool {
	for _, enc := range strings.Split(accept, ",") {
		enc, _, _ = strings.Cut(enc, ";") // drop any q-weight
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			return true
		}
	}
	return false
}

// writeText writes a 200 response with a plain-text body, naming the encoding
// only when the client offered it. A server must never apply an encoding the
// client did not ask for, so an unrecognised value means no header at all.
func writeText(conn net.Conn, body, acceptEncoding string, keepAlive bool) {
	payload := []byte(body)
	encoding := ""
	if acceptsGzip(acceptEncoding) {
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		w.Write(payload)
		// Close writes the gzip trailer, so the length is only correct
		// afterwards — a deferred Close would run too late to measure.
		w.Close()
		payload = gz.Bytes()
		encoding = "Content-Encoding: gzip\r\n"
	}
	// Content-Length describes the bytes actually sent, compressed or not.
	closing := ""
	if !keepAlive {
		closing = "Connection: close\r\n"
	}
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\n%s%sContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n",
		encoding, closing, len(payload))
	conn.Write(payload)
}
