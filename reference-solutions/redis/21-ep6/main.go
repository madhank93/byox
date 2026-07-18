package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var config = map[string]string{
	"dir":            "",
	"dbfilename":     "",
	"appendonly":     "no",
	"appenddirname":  "appendonlydir",
	"appendfilename": "appendonly.aof",
	"appendfsync":    "everysec",
}

var (
	aofMu   sync.Mutex
	aofPath string
)

func main() {
	cwd, _ := os.Getwd()
	config["dir"] = cwd

	dir := flag.String("dir", "", "data directory")
	dbfilename := flag.String("dbfilename", "", "RDB filename")
	appendonly := flag.String("appendonly", "", "enable AOF")
	appenddirname := flag.String("appenddirname", "", "AOF subdirectory")
	appendfilename := flag.String("appendfilename", "", "AOF filename")
	appendfsync := flag.String("appendfsync", "", "AOF fsync policy")
	flag.Parse()
	setIf("dir", *dir)
	setIf("dbfilename", *dbfilename)
	setIf("appendonly", *appendonly)
	setIf("appenddirname", *appenddirname)
	setIf("appendfilename", *appendfilename)
	setIf("appendfsync", *appendfsync)

	if config["dbfilename"] != "" {
		loadRDB(filepath.Join(config["dir"], config["dbfilename"]))
	}
	if config["appendonly"] == "yes" {
		setupAOF()
	}

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
		resp := execute(args)
		if config["appendonly"] == "yes" && isWriteCommand(args[0]) {
			appendAOF(args)
		}
		conn.Write(resp)
	}
}

func setIf(key, val string) {
	if val != "" {
		config[key] = val
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
	case "CONFIG":
		return cmdConfig(args)
	case "KEYS":
		return cmdKeys(args)
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

func cmdConfig(args []string) []byte {
	if len(args) >= 3 && strings.ToUpper(args[1]) == "GET" {
		key := strings.ToLower(args[2])
		val, ok := config[key]
		if !ok {
			return arrayOf()
		}
		return arrayOf(bulkString(key), bulkString(val))
	}
	return errResp("unsupported CONFIG subcommand")
}

func cmdKeys(args []string) []byte {
	mu.Lock()
	defer mu.Unlock()
	var parts [][]byte
	for k, e := range store {
		if expired(e) {
			continue
		}
		parts = append(parts, bulkString(k))
	}
	return arrayOf(parts...)
}

func arrayOf(parts ...[]byte) []byte {
	out := []byte(fmt.Sprintf("*%d\r\n", len(parts)))
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// --- RDB loading (string values only, with optional expiry) ---

func loadRDB(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReader(f)

	header := make([]byte, 9)
	if _, err := io.ReadFull(r, header); err != nil || string(header[:5]) != "REDIS" {
		return
	}

	var pendingExpiry time.Time
	for {
		op, err := r.ReadByte()
		if err != nil {
			return
		}
		switch op {
		case 0xFF:
			return
		case 0xFE:
			readLength(r)
		case 0xFB:
			readLength(r)
			readLength(r)
		case 0xFA:
			readRDBString(r)
			readRDBString(r)
		case 0xFC:
			var ms uint64
			binary.Read(r, binary.LittleEndian, &ms)
			pendingExpiry = time.UnixMilli(int64(ms))
		case 0xFD:
			var sec uint32
			binary.Read(r, binary.LittleEndian, &sec)
			pendingExpiry = time.Unix(int64(sec), 0)
		case 0x00:
			key, _ := readRDBString(r)
			val, _ := readRDBString(r)
			e := entry{value: val}
			if !pendingExpiry.IsZero() {
				e.expireAt = pendingExpiry
			}
			store[key] = e
			pendingExpiry = time.Time{}
		default:
			return
		}
	}
}

func readLength(r *bufio.Reader) (int, bool) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, false
	}
	switch b >> 6 {
	case 0:
		return int(b & 0x3F), false
	case 1:
		b2, _ := r.ReadByte()
		return int(b&0x3F)<<8 | int(b2), false
	case 2:
		if b == 0x80 {
			var n uint32
			binary.Read(r, binary.BigEndian, &n)
			return int(n), false
		}
		var n uint64
		binary.Read(r, binary.BigEndian, &n)
		return int(n), false
	default:
		return int(b & 0x3F), true
	}
}

func readRDBString(r *bufio.Reader) (string, error) {
	n, special := readLength(r)
	if special {
		switch n {
		case 0:
			b, _ := r.ReadByte()
			return strconv.Itoa(int(int8(b))), nil
		case 1:
			var v int16
			binary.Read(r, binary.LittleEndian, &v)
			return strconv.Itoa(int(v)), nil
		case 2:
			var v int32
			binary.Read(r, binary.LittleEndian, &v)
			return strconv.Itoa(int(v)), nil
		default:
			return "", fmt.Errorf("unsupported special string encoding %d", n)
		}
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

var writeCommands = map[string]bool{"SET": true, "DEL": true, "INCR": true}

func isWriteCommand(name string) bool {
	return writeCommands[strings.ToUpper(name)]
}

// --- AOF persistence ---

func setupAOF() {
	dir := filepath.Join(config["dir"], config["appenddirname"])
	os.MkdirAll(dir, 0o755)
	manifest := filepath.Join(dir, config["appendfilename"]+".manifest")

	if data, err := os.ReadFile(manifest); err == nil {
		// Existing manifest: find the active incremental file and replay it.
		if incr := activeIncrFile(string(data)); incr != "" {
			aofPath = filepath.Join(dir, incr)
			replayAOF(aofPath)
			return
		}
	}
	// Fresh setup: create the first incremental file + manifest.
	incr := config["appendfilename"] + ".1.incr.aof"
	aofPath = filepath.Join(dir, incr)
	os.WriteFile(aofPath, nil, 0o644)
	os.WriteFile(manifest, []byte(fmt.Sprintf("file %s seq 1 type i\n", incr)), 0o644)
}

// activeIncrFile returns the filename of the last "type i" entry.
func activeIncrFile(manifest string) string {
	var incr string
	for _, line := range strings.Split(manifest, "\n") {
		f := strings.Fields(line)
		var name, typ string
		for i := 0; i+1 < len(f); i += 2 {
			switch f[i] {
			case "file":
				name = f[i+1]
			case "type":
				typ = f[i+1]
			}
		}
		if typ == "i" && name != "" {
			incr = name
		}
	}
	return incr
}

func replayAOF(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) > 0 {
			execute(args) // rebuild state without re-logging
		}
	}
}

func appendAOF(args []string) {
	aofMu.Lock()
	defer aofMu.Unlock()
	if aofPath == "" {
		return
	}
	f, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(encodeCommand(args))
}

func encodeCommand(args []string) []byte {
	out := []byte(fmt.Sprintf("*%d\r\n", len(args)))
	for _, a := range args {
		out = append(out, bulkString(a)...)
	}
	return out
}
