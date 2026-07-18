package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
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

type request struct {
	method  string
	path    string
	headers map[string]string
	body    []byte
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		req, err := readRequest(r)
		if err != nil {
			return
		}
		keepAlive := !strings.EqualFold(req.headers["connection"], "close")
		respond(conn, req, keepAlive)
		if !keepAlive {
			return
		}
	}
}

func readRequest(r *bufio.Reader) (*request, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 {
		return nil, fmt.Errorf("bad request line")
	}
	req := &request{method: parts[0], path: parts[1], headers: map[string]string{}}
	for {
		h, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" {
			break
		}
		k, v, ok := strings.Cut(h, ":")
		if ok {
			req.headers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if cl := req.headers["content-length"]; cl != "" {
		n, _ := strconv.Atoi(cl)
		req.body = make([]byte, n)
		if _, err := readFull(r, req.body); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func respond(conn net.Conn, req *request, keepAlive bool) {
	switch {
	case req.path == "/":
		writeResponse(conn, req, 200, "", nil, keepAlive)

	case strings.HasPrefix(req.path, "/echo/"):
		writeResponse(conn, req, 200, "text/plain", []byte(strings.TrimPrefix(req.path, "/echo/")), keepAlive)

	case req.path == "/user-agent":
		writeResponse(conn, req, 200, "text/plain", []byte(req.headers["user-agent"]), keepAlive)

	case strings.HasPrefix(req.path, "/files/"):
		name := strings.TrimPrefix(req.path, "/files/")
		full := filepath.Join(fileDir, name)
		if req.method == "POST" {
			if err := os.WriteFile(full, req.body, 0o644); err != nil {
				writeResponse(conn, req, 500, "", nil, keepAlive)
			} else {
				writeResponse(conn, req, 201, "", nil, keepAlive)
			}
		} else {
			data, err := os.ReadFile(full)
			if err != nil {
				writeResponse(conn, req, 404, "", nil, keepAlive)
			} else {
				writeResponse(conn, req, 200, "application/octet-stream", data, keepAlive)
			}
		}

	default:
		writeResponse(conn, req, 404, "", nil, keepAlive)
	}
}

var statusText = map[int]string{200: "OK", 201: "Created", 404: "Not Found", 500: "Internal Server Error"}

func writeResponse(conn net.Conn, req *request, status int, contentType string, body []byte, keepAlive bool) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, statusText[status])

	if body != nil && wantsGzip(req.headers["accept-encoding"]) {
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		w.Write(body)
		w.Close()
		body = gz.Bytes()
		b.WriteString("Content-Encoding: gzip\r\n")
	}
	if contentType != "" {
		fmt.Fprintf(&b, "Content-Type: %s\r\n", contentType)
	}
	if body != nil {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	}
	if !keepAlive {
		b.WriteString("Connection: close\r\n")
	}
	b.WriteString("\r\n")
	if body != nil {
		b.Write(body)
	}
	conn.Write(b.Bytes())
}

// wantsGzip reports whether the Accept-Encoding list includes gzip.
func wantsGzip(accept string) bool {
	for _, enc := range strings.Split(accept, ",") {
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			return true
		}
	}
	return false
}
