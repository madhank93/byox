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

// config holds the server's settings, seeded with defaults and overridden by
// command-line flags. CONFIG GET reads from it.
var config = map[string]string{
	"dir":            "",
	"dbfilename":     "",
	"appendonly":     "no",
	"appenddirname":  "appendonlydir",
	"appendfilename": "appendonly.aof",
	"appendfsync":    "everysec",
}

func main() {
	dir := flag.String("dir", "", "directory holding the RDB file")
	dbfilename := flag.String("dbfilename", "", "RDB file name")
	appendonly := flag.String("appendonly", "no", "enable the append-only file")
	appenddirname := flag.String("appenddirname", "appendonlydir", "AOF directory name")
	appendfilename := flag.String("appendfilename", "appendonly.aof", "AOF base name")
	appendfsync := flag.String("appendfsync", "everysec", "AOF fsync policy")
	port := flag.Int("port", 6379, "port to listen on")
	replicaof := flag.String("replicaof", "", "\"<host> <port>\" of the master to replicate")
	flag.Parse()
	config["port"] = strconv.Itoa(*port)
	config["replicaof"] = *replicaof
	config["appendfsync"] = *appendfsync
	config["appendonly"] = *appendonly
	config["appenddirname"] = *appenddirname
	config["appendfilename"] = *appendfilename
	config["dir"] = *dir
	if config["dir"] == "" {
		// With no --dir, Redis reports the directory it was started in.
		if wd, err := os.Getwd(); err == nil {
			config["dir"] = wd
		}
	}
	config["dbfilename"] = *dbfilename
	loadRDB(filepath.Join(config["dir"], config["dbfilename"]))
	if strings.EqualFold(config["appendonly"], "yes") {
		setupAOF()
	}

	l, err := net.Listen("tcp", "0.0.0.0:"+config["port"])
	if err != nil {
		fmt.Println("Failed to bind to port " + config["port"])
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
		execute(conn, args)
	}
}

// execute runs one command, writing its reply to w. Replay feeds the AOF
// through this same function with the replies discarded, so a restored server
// and a live one can never diverge through a second code path.
func execute(w io.Writer, args []string) {
	{
		conn := w
		switch strings.ToUpper(args[0]) {
		case "PING":
			conn.Write([]byte("+PONG\r\n"))
		case "ECHO":
			if len(args) > 1 {
				conn.Write(bulkString(args[1]))
			}
		case "SET":
			if len(args) > 2 {
				appendAOF(args)
				e := entry{value: args[2]}
				if ms, ok := pxOption(args); ok {
					e.expireAt = time.Now().Add(time.Duration(ms) * time.Millisecond)
				}
				mu.Lock()
				store[args[1]] = e
				mu.Unlock()
				conn.Write([]byte("+OK\r\n"))
			}
		case "KEYS":
			mu.Lock()
			parts := make([][]byte, 0, len(store))
			for k := range store {
				parts = append(parts, bulkString(k))
			}
			mu.Unlock()
			conn.Write(arrayOf(parts...))
		case "INFO":
			// INFO answers with one bulk string of newline-separated
			// key:value lines, not an array — RESP2 has no map type.
			lines := []string{"# Replication", "role:" + serverRole()}
			conn.Write(bulkString(strings.Join(lines, "\r\n")))
		case "CONFIG":
			if len(args) > 2 && strings.EqualFold(args[1], "GET") {
				// RESP2 has no map type, so CONFIG GET answers with a flat
				// array of name, value pairs.
				conn.Write(arrayOf(bulkString(args[2]), bulkString(config[strings.ToLower(args[2])])))
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
					return
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

// setupAOF prepares the append-only directory. Creating it is idempotent: a
// server restarts against its own existing data far more often than it starts
// fresh.
// aofPath is the incremental AOF this server appends to.
var (
	aofPath string
	aofMu   sync.Mutex
)

func setupAOF() {
	dir := filepath.Join(config["dir"], config["appenddirname"])
	os.MkdirAll(dir, 0o755)

	// The manifest names the files that make up the AOF, so recovery follows
	// it rather than a directory listing — and the incremental file it names
	// need not share the configured base name.
	manifest := filepath.Join(dir, config["appendfilename"]+".manifest")
	if data, err := os.ReadFile(manifest); err == nil {
		if incr := activeIncrFile(string(data)); incr != "" {
			aofPath = filepath.Join(dir, incr)
			replayAOF(aofPath)
			return
		}
	}

	// Fresh setup: create the first incremental file and the manifest naming
	// it. Append mode, because truncating on open would destroy exactly the
	// data the AOF exists to protect.
	incr := config["appendfilename"] + ".1.incr.aof"
	aofPath = filepath.Join(dir, incr)
	f, err := os.OpenFile(aofPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	f.Close()
	os.WriteFile(manifest, []byte(fmt.Sprintf("file %s seq 1 type i\n", incr)), 0o644)
}

// activeIncrFile returns the file named by the manifest's last "type i" entry.
func activeIncrFile(manifest string) string {
	var incr string
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		var name, kind string
		for i := 0; i+1 < len(fields); i += 2 {
			switch fields[i] {
			case "file":
				name = fields[i+1]
			case "type":
				kind = fields[i+1]
			}
		}
		if kind == "i" && name != "" {
			incr = name
		}
	}
	return incr
}

// replayAOF rebuilds the keyspace from the log at startup, before any client
// connects. Replayed commands must not be logged again, so the append path is
// disabled for the duration.
func replayAOF(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	saved := aofPath
	aofPath = "" // suppress logging while replaying
	defer func() { aofPath = saved }()

	r := bufio.NewReader(f)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) > 0 {
			execute(io.Discard, args)
		}
	}
}

// appendAOF logs a command. The log format is RESP — the same encoding the
// client sent — so replay is the parser read backwards, with no second format
// to keep in sync.
func appendAOF(args []string) {
	if aofPath == "" || len(args) == 0 || !isWriteCommand(args[0]) {
		return
	}
	aofMu.Lock()
	defer aofMu.Unlock()
	f, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(encodeCommand(args))
}

// writeCommands classifies the command set. Only commands that change state
// belong in the log — logging reads inflates every restart and replays to the
// same keyspace anyway.
var writeCommands = map[string]bool{"SET": true}

func isWriteCommand(name string) bool {
	return writeCommands[strings.ToUpper(name)]
}

// serverRole reports whether this server replicates from another.
func serverRole() string {
	if config["replicaof"] != "" {
		return "slave"
	}
	return "master"
}

func encodeCommand(args []string) []byte {
	parts := make([][]byte, len(args))
	for i, a := range args {
		parts[i] = bulkString(a)
	}
	return arrayOf(parts...)
}

// loadRDB reads the snapshot into the keyspace. The file is a stream of
// opcode-introduced sections: 0xFE selects a database, 0xFB gives hash table
// sizes, 0x00 introduces a string key/value pair, and 0xFF ends the file.
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
	// An expiry opcode precedes the key/value pair it applies to, so it is
	// carried forward to the next 0x00 section.
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
			// Auxiliary metadata (redis-ver, redis-bits, …) precedes the
			// data; read past both halves of the pair.
			readRDBString(r)
			readRDBString(r)
		case 0xFC:
			// Expiry in milliseconds. RDB is a disk format, so its integers
			// are little-endian — unlike the network formats elsewhere here.
			var ms uint64
			binary.Read(r, binary.LittleEndian, &ms)
			pendingExpiry = time.UnixMilli(int64(ms))
		case 0xFD:
			var sec uint32
			binary.Read(r, binary.LittleEndian, &sec)
			pendingExpiry = time.Unix(int64(sec), 0)
		case 0x00:
			key, err := readRDBString(r)
			if err != nil {
				return
			}
			val, err := readRDBString(r)
			if err != nil {
				return
			}
			store[key] = entry{value: val, expireAt: pendingExpiry}
			pendingExpiry = time.Time{}
		default:
			return
		}
	}
}

// readLength decodes RDB's length encoding. The top two bits of the first
// byte pick the format, and 0b11 does not introduce a length at all — it
// flags a special integer encoding, which is what the bool reports.
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
		var n uint32
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

func arrayOf(parts ...[]byte) []byte {
	out := []byte(fmt.Sprintf("*%d\r\n", len(parts)))
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func bulkString(s string) []byte {
	return []byte("$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n")
}
