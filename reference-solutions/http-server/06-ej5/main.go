package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
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
	r := bufio.NewReader(conn)
	line, _ := r.ReadString('\n')
	parts := strings.Fields(line)
	path := "/"
	if len(parts) >= 2 {
		path = parts[1]
	}
	headers := readHeaders(r)

	switch {
	case path == "/":
		conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
	case strings.HasPrefix(path, "/echo/"):
		writeText(conn, strings.TrimPrefix(path, "/echo/"))
	case path == "/user-agent":
		writeText(conn, headers["user-agent"])
	default:
		conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))
	}
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

// writeText writes a 200 response with a plain-text body.
func writeText(conn net.Conn, body string) {
	fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}
