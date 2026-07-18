package main

import (
	"bufio"
	"fmt"
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
	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go handleConn(conn)
	}
}

type entry struct {
	value    string
	expireAt time.Time // zero = no expiry
}

var (
	mu    sync.Mutex
	store = map[string]entry{}
)

func handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		conn.Write(execute(args))
	}
}

// readCommand reads one RESP array of bulk strings (the client protocol).
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("expected array, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		bulk, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if len(bulk) == 0 || bulk[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got %q", bulk)
		}
		length, err := strconv.Atoi(bulk[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, length+2) // include trailing CRLF
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:length]))
	}
	return args, nil
}

func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
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

func execute(args []string) []byte {
	switch strings.ToUpper(args[0]) {
	case "PING":
		return []byte("+PONG\r\n")
	case "ECHO":
		if len(args) < 2 {
			return errResp("wrong number of arguments for 'echo'")
		}
		return bulkString(args[1])
	case "SET":
		return cmdSet(args)
	case "GET":
		return cmdGet(args)
	default:
		return errResp("unknown command '" + args[0] + "'")
	}
}

func cmdSet(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'set'")
	}
	e := entry{value: args[2]}
	// Optional expiry: PX <milliseconds>
	for i := 3; i+1 < len(args); i += 2 {
		if strings.ToUpper(args[i]) == "PX" {
			ms, err := strconv.Atoi(args[i+1])
			if err != nil {
				return errResp("value is not an integer or out of range")
			}
			e.expireAt = time.Now().Add(time.Duration(ms) * time.Millisecond)
		}
	}
	mu.Lock()
	store[args[1]] = e
	mu.Unlock()
	return []byte("+OK\r\n")
}

func cmdGet(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'get'")
	}
	mu.Lock()
	e, ok := store[args[1]]
	if ok && expired(e) {
		delete(store, args[1])
		ok = false
	}
	mu.Unlock()
	if !ok {
		return nullBulk()
	}
	return bulkString(e.value)
}

func expired(e entry) bool {
	return !e.expireAt.IsZero() && time.Now().After(e.expireAt)
}

// --- RESP encoding helpers ---

func bulkString(s string) []byte {
	return []byte(fmt.Sprintf("$%d\r\n%s\r\n", len(s), s))
}

func nullBulk() []byte {
	return []byte("$-1\r\n")
}

func errResp(msg string) []byte {
	return []byte("-ERR " + msg + "\r\n")
}
