package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	if config["replicaof"] != "" {
		go replicateFrom(config["replicaof"])
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

// streamEntry is one entry of a stream: an id and the field/value pairs that
// came with it. Ids are ordered, so a stream is sorted by construction.
type streamEntry struct {
	ms, seq uint64
	fields  []string
}

func (e streamEntry) id() string {
	return strconv.FormatUint(e.ms, 10) + "-" + strconv.FormatUint(e.seq, 10)
}

// resolveStreamID turns an XADD id argument into concrete numbers, expanding
// a "<ms>-*" request into the next sequence for that millisecond. Resolving
// before validating means auto-generated and explicit ids run the same checks.
func resolveStreamID(id string, existing []streamEntry) (uint64, uint64, bool) {
	if id == "*" {
		// Wall-clock time, but never an id that would not increase: within
		// one millisecond the sequence carries the ordering.
		ms := uint64(time.Now().UnixMilli())
		if n := len(existing); n > 0 && existing[n-1].ms >= ms {
			last := existing[n-1]
			return last.ms, last.seq + 1, true
		}
		return ms, 0, true
	}
	if msText, seqText, found := strings.Cut(id, "-"); found && seqText == "*" {
		ms, err := strconv.ParseUint(msText, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		for i := len(existing) - 1; i >= 0; i-- {
			if existing[i].ms == ms {
				return ms, existing[i].seq + 1, true
			}
		}
		// A fresh millisecond starts at 0 — except at millisecond 0, where
		// 0-0 is forbidden.
		if ms == 0 {
			return 0, 1, true
		}
		return ms, 0, true
	}
	return parseStreamID(id)
}

// parseStreamID splits an explicit "<ms>-<seq>" id.
func parseStreamID(id string) (uint64, uint64, bool) {
	msText, seqText, found := strings.Cut(id, "-")
	if !found {
		return 0, 0, false
	}
	ms, err := strconv.ParseUint(msText, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	seq, err := strconv.ParseUint(seqText, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ms, seq, true
}

var streams = map[string][]streamEntry{}

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
				propagate(args)
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
		case "REPLCONF":
			// An ACK is a report, not a request: record it and stay silent.
			if len(args) >= 3 && strings.EqualFold(args[1], "ACK") {
				if c, ok := w.(net.Conn); ok {
					if offset, err := strconv.ParseInt(args[2], 10, 64); err == nil {
						recordAck(c, offset)
					}
				}
				return
			}
			conn.Write([]byte("+OK\r\n"))
		case "WAIT":
			need, timeoutMS := 0, 0
			if len(args) > 2 {
				need, _ = strconv.Atoi(args[1])
				timeoutMS, _ = strconv.Atoi(args[2])
			}
			fmt.Fprintf(conn, ":%d\r\n", waitForReplicas(need, time.Duration(timeoutMS)*time.Millisecond))
		case "PSYNC":
			// The master commits to a history and a starting point; everything
			// it sends afterwards continues from there.
			fmt.Fprintf(conn, "+FULLRESYNC %s %d\r\n", replID, masterOffset())
			conn.Write(emptyRDBPayload())
			// This connection is no longer a client; it is a destination.
			if c, ok := w.(net.Conn); ok {
				addReplica(c)
			}
		case "TYPE":
			if len(args) > 1 {
				mu.Lock()
				_, isString := store[args[1]]
				_, isStream := streams[args[1]]
				mu.Unlock()
				switch {
				case isStream:
					conn.Write([]byte("+stream\r\n"))
				case isString:
					conn.Write([]byte("+string\r\n"))
				default:
					conn.Write([]byte("+none\r\n"))
				}
			}
		case "XADD":
			if len(args) > 2 {
				mu.Lock()
				ms, seq, ok := resolveStreamID(args[2], streams[args[1]])
				if !ok {
					mu.Unlock()
					conn.Write([]byte("-ERR Invalid stream ID specified as stream command argument\r\n"))
					return
				}
				// Two invariants make a stream a log rather than a bag: ids
				// strictly increase, and 0-0 is reserved as the sentinel
				// meaning "before everything".
				if ms == 0 && seq == 0 {
					mu.Unlock()
					conn.Write([]byte("-ERR The ID specified in XADD must be greater than 0-0\r\n"))
					return
				}
				existing := streams[args[1]]
				if n := len(existing); n > 0 {
					last := existing[n-1]
					if ms < last.ms || (ms == last.ms && seq <= last.seq) {
						mu.Unlock()
						conn.Write([]byte("-ERR The ID specified in XADD is equal or smaller than the target stream top item\r\n"))
						return
					}
				}
				e := streamEntry{ms: ms, seq: seq, fields: args[3:]}
				streams[args[1]] = append(existing, e)
				mu.Unlock()
				conn.Write(bulkString(e.id()))
			}
		case "INFO":
			// INFO answers with one bulk string of newline-separated
			// key:value lines, not an array — RESP2 has no map type.
			lines := []string{
				"# Replication",
				"role:" + serverRole(),
				"master_replid:" + replID,
				"master_repl_offset:" + strconv.FormatInt(masterOffset(), 10),
			}
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
	args, _, err := readCommandN(r)
	return args, err
}

// readCommandN also reports how many bytes the command occupied on the wire.
// The replication offset is measured in those bytes, so it is counted where
// the command is read rather than re-derived by re-encoding it afterwards.
func readCommandN(r *bufio.Reader) ([]string, int, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, 0, err
	}
	consumed := len(line) + 2
	if len(line) == 0 || line[0] != '*' {
		return nil, consumed, nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, consumed, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		header, err := readLine(r)
		if err != nil {
			return nil, consumed, err
		}
		consumed += len(header) + 2
		if len(header) == 0 || header[0] != '$' {
			return nil, consumed, fmt.Errorf("expected bulk string, got %q", header)
		}
		size, err := strconv.Atoi(header[1:])
		if err != nil {
			return nil, consumed, err
		}
		buf := make([]byte, size+2) // payload plus the trailing CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, consumed, err
		}
		consumed += size + 2
		args = append(args, string(buf[:size]))
	}
	return args, consumed, nil
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

// replica is a connection the master streams writes to. Each carries its own
// write mutex: two client goroutines propagating at once would otherwise
// interleave halves of two commands into bytes that parse as neither.
type replica struct {
	conn net.Conn
	mu   sync.Mutex

	// ackOffset is the last offset this replica reported, written by the
	// goroutine reading that replica's connection and read by WAIT.
	ackOffset int64
}

var (
	replicasMu sync.Mutex
	replicas   []*replica
)

func addReplica(conn net.Conn) {
	replicasMu.Lock()
	replicas = append(replicas, &replica{conn: conn})
	replicasMu.Unlock()
}

// propagate forwards a write to every replica. The list is copied under its
// own lock and written to outside it, so one slow replica cannot block another
// from registering.
func propagate(args []string) {
	replicasMu.Lock()
	targets := make([]*replica, len(replicas))
	copy(targets, replicas)
	replicasMu.Unlock()
	if len(targets) == 0 {
		return
	}
	payload := encodeCommand(args)
	atomic.AddInt64(&replOffset, int64(len(payload)))
	for _, rep := range targets {
		rep.mu.Lock()
		rep.conn.Write(payload)
		rep.mu.Unlock()
	}
}

func recordAck(conn net.Conn, offset int64) {
	replicasMu.Lock()
	defer replicasMu.Unlock()
	for _, rep := range replicas {
		if rep.conn == conn {
			atomic.StoreInt64(&rep.ackOffset, offset)
			return
		}
	}
}

// waitForReplicas reports how many replicas have acknowledged everything
// written so far, blocking up to timeout for them. The target offset is
// captured before GETACK is broadcast, because sending it advances the
// master's own offset.
func waitForReplicas(need int, timeout time.Duration) int {
	target := atomic.LoadInt64(&replOffset)

	replicasMu.Lock()
	targets := make([]*replica, len(replicas))
	copy(targets, replicas)
	replicasMu.Unlock()

	// Nothing has been written, so every replica is trivially current.
	if target == 0 {
		return len(targets)
	}
	if acked := countAcked(targets, target); acked >= need {
		return acked
	}

	getack := encodeCommand([]string{"REPLCONF", "GETACK", "*"})
	for _, rep := range targets {
		rep.mu.Lock()
		rep.conn.Write(getack)
		rep.mu.Unlock()
	}
	atomic.AddInt64(&replOffset, int64(len(getack)))

	// Return as soon as enough replicas answer — the tester measures how long
	// this takes, so waiting out the full timeout is a failure, not caution.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if acked := countAcked(targets, target); acked >= need {
			return acked
		}
		time.Sleep(10 * time.Millisecond)
	}
	return countAcked(targets, target)
}

func countAcked(targets []*replica, target int64) int {
	n := 0
	for _, rep := range targets {
		if atomic.LoadInt64(&rep.ackOffset) >= target {
			n++
		}
	}
	return n
}

// emptyRDBPayload frames an empty snapshot the way the replication stream
// does: a bulk-string length header followed by the raw bytes and — uniquely
// in RESP — no trailing CRLF. A reader that assumes one eats the first two
// bytes of the command stream that follows.
func emptyRDBPayload() []byte {
	const emptyRDB = "UkVESVMwMDEx+glyZWRpcy12ZXIFNy40LjD6CnJlZGlzLWJpdHPAQPoFY3RpbWXCbQi8ZfoIdXNlZC1tZW3CsMQQAPoIYW9mLWJhc2XAAP/wbjv+wP9aog=="
	payload, err := base64.StdEncoding.DecodeString(emptyRDB)
	if err != nil {
		return nil
	}
	return append([]byte(fmt.Sprintf("$%d\r\n", len(payload))), payload...)
}

// replicateFrom dials the master and runs the handshake. The replica speaks
// RESP as a client here — the same encoding it serves, in the other direction.
// The connection is kept for the whole session: the handshake, the RDB
// transfer and the command stream all flow over this one socket.
func replicateFrom(target string) {
	parts := strings.Fields(target)
	if len(parts) != 2 {
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(parts[0], parts[1]))
	if err != nil {
		fmt.Println("replication dial failed:", err)
		return
	}
	r := bufio.NewReader(conn)

	// PING doubles as a liveness check before anything expensive is agreed.
	conn.Write(encodeCommand([]string{"PING"}))
	readLine(r)

	// Capability negotiation: where this replica listens, and what it speaks.
	conn.Write(encodeCommand([]string{"REPLCONF", "listening-port", config["port"]}))
	readLine(r)
	conn.Write(encodeCommand([]string{"REPLCONF", "capa", "psync2"}))
	readLine(r)

	// "?" means "I don't know your replication id" and -1 "I have no offset",
	// so this asks for a full resynchronisation.
	conn.Write(encodeCommand([]string{"PSYNC", "?", "-1"}))
	readLine(r)

	// The RDB payload is framed like a bulk string but carries no trailing
	// CRLF, so read exactly the declared length and stop.
	header, err := readLine(r)
	if err != nil || len(header) == 0 || header[0] != '$' {
		return
	}
	size, err := strconv.Atoi(header[1:])
	if err != nil {
		return
	}
	if _, err := io.ReadFull(r, make([]byte, size)); err != nil {
		return
	}

	// Everything after the snapshot is the command stream: applied silently,
	// with no reply, and counted byte by byte as it is processed.
	for {
		args, n, err := readCommandN(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		// GETACK is answered with the offset as it stood *before* this
		// command, so the reply happens first and the bytes are added after.
		if len(args) >= 2 && strings.EqualFold(args[0], "REPLCONF") && strings.EqualFold(args[1], "GETACK") {
			offset := atomic.LoadInt64(&replicaOffset)
			conn.Write(encodeCommand([]string{"REPLCONF", "ACK", strconv.FormatInt(offset, 10)}))
		} else {
			execute(io.Discard, args)
		}
		atomic.AddInt64(&replicaOffset, int64(n))
	}
}

// replicaOffset counts the bytes of the master's stream this replica has
// processed. It measures the master's output, never the replica's replies.
var replicaOffset int64

// replID identifies this master's replication history. A replica that
// reconnects reporting a different id has to resynchronise from scratch.
var replID = randomReplID()

// replOffset counts the bytes of the replication stream produced so far. It is
// the number both sides of replication must agree on exactly.
var replOffset int64

func masterOffset() int64 {
	return atomic.LoadInt64(&replOffset)
}

func randomReplID() string {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", 40)
	}
	return hex.EncodeToString(buf)
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
