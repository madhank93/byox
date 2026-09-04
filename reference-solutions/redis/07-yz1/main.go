package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}
		go handleConn(conn)
	}
}

// The keyspace is shared by every connection goroutine, so every read and
// write of it happens under mu.
var (
	mu    sync.Mutex
	store = map[string]entry{}
)

// entry is a stored value with an optional deadline. Expiry is lazy: nothing
// sweeps the keyspace, a read treats an expired key as missing.
type entry struct {
	value    string
	expireAt time.Time // zero means no expiry
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		args, err := readCommand(r)
		if err != nil {
			if err != io.EOF {
				fmt.Println("read error:", err)
			}
			return
		}
		if len(args) == 0 {
			continue
		}
		switch strings.ToUpper(args[0]) {
		case "PING":
			conn.Write([]byte("+PONG\r\n"))
		case "ECHO":
			if len(args) > 1 {
				conn.Write(bulkString(args[1]))
			}
		case "SET":
			if len(args) > 2 {
				e := entry{value: args[2]}
				if ms, ok := pxOption(args); ok {
					e.expireAt = time.Now().Add(time.Duration(ms) * time.Millisecond)
				}
				mu.Lock()
				store[args[1]] = e
				mu.Unlock()
				conn.Write([]byte("+OK\r\n"))
			}
		case "GET":
			if len(args) > 1 {
				mu.Lock()
				e, ok := store[args[1]]
				if ok && !e.expireAt.IsZero() && time.Now().After(e.expireAt) {
					delete(store, args[1])
					ok = false
				}
				mu.Unlock()
				if !ok {
					conn.Write([]byte("$-1\r\n"))
					continue
				}
				conn.Write(bulkString(e.value))
			}
		}
	}
}

// readCommand reads one RESP array of bulk strings — the encoding every
// client uses to send a command — and returns its arguments. Bulk strings are
// binary-safe, so the declared length decides how much to read, never a
// delimiter.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		header, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if len(header) == 0 || header[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got %q", header)
		}
		size, err := strconv.Atoi(header[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2) // payload plus the trailing CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

// pxOption finds a PX <milliseconds> option among a SET command's trailing
// arguments.
func pxOption(args []string) (int, bool) {
	for i := 3; i+1 < len(args); i++ {
		if strings.EqualFold(args[i], "PX") {
			ms, err := strconv.Atoi(args[i+1])
			if err != nil {
				return 0, false
			}
			return ms, true
		}
	}
	return 0, false
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func bulkString(s string) []byte {
	return []byte("$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n")
}
