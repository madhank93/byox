package main

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
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
	"port":           "6379",
}

// Replication state.
var (
	replicaOf  string
	replID     = "8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb"
	replOffset int
)

func isReplica() bool { return replicaOf != "" }

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
	port := flag.String("port", "", "listening port")
	replof := flag.String("replicaof", "", "master (\"host port\")")
	flag.Parse()
	setIf("dir", *dir)
	setIf("dbfilename", *dbfilename)
	setIf("appendonly", *appendonly)
	setIf("appenddirname", *appenddirname)
	setIf("appendfilename", *appendfilename)
	setIf("appendfsync", *appendfsync)
	setIf("port", *port)
	replicaOf = *replof

	if config["dbfilename"] != "" {
		loadRDB(filepath.Join(config["dir"], config["dbfilename"]))
	}
	if config["appendonly"] == "yes" {
		setupAOF()
	}
	if isReplica() {
		go replicaHandshake()
	}

	l, err := net.Listen("tcp", "0.0.0.0:"+config["port"])
	if err != nil {
		fmt.Println("Failed to bind to port " + config["port"])
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
	mu       sync.Mutex
	store    = map[string]entry{}
	versions = map[string]int64{} // per-key modification counter (for WATCH)
)

func touch(key string) { versions[key]++ }

// clientState holds per-connection transaction/watch/subscription state.
type clientState struct {
	conn     net.Conn
	wmu      sync.Mutex // serializes writes (own replies vs pushed messages)
	inMulti  bool
	queued   [][]string
	watching map[string]int64
	subs     map[string]bool
}

func (cs *clientState) write(b []byte) {
	cs.wmu.Lock()
	cs.conn.Write(b)
	cs.wmu.Unlock()
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	cs := &clientState{conn: conn}
	defer unsubscribeAll(cs)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		name := strings.ToUpper(args[0])
		if name == "PSYNC" {
			conn.Write([]byte("+FULLRESYNC " + replID + " 0\r\n"))
			conn.Write(emptyRDBPayload())
			serveReplica(conn, r)
			return
		}
		cs.write(handleCommand(cs, name, args))
	}
}

func handleCommand(cs *clientState, name string, args []string) []byte {
	// Subscribed mode allows only a small command subset.
	if len(cs.subs) > 0 && !subModeAllowed[name] {
		return errResp(fmt.Sprintf("Can't execute '%s': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context", strings.ToLower(name)))
	}
	switch name {
	case "SUBSCRIBE":
		return cmdSubscribe(cs, args)
	case "UNSUBSCRIBE":
		return cmdUnsubscribe(cs, args)
	case "PUBLISH":
		return cmdPublish(args)
	case "PING":
		if len(cs.subs) > 0 {
			return arrayOf(bulkString("pong"), bulkString(""))
		}
		return []byte("+PONG\r\n")
	case "MULTI":
		cs.inMulti = true
		cs.queued = nil
		return []byte("+OK\r\n")
	case "DISCARD":
		if !cs.inMulti {
			return errResp("DISCARD without MULTI")
		}
		cs.inMulti = false
		cs.queued = nil
		cs.watching = nil
		return []byte("+OK\r\n")
	case "EXEC":
		if !cs.inMulti {
			return errResp("EXEC without MULTI")
		}
		cs.inMulti = false
		queued := cs.queued
		cs.queued = nil
		aborted := watchViolated(cs)
		cs.watching = nil
		if aborted {
			return nullArray()
		}
		out := []byte(fmt.Sprintf("*%d\r\n", len(queued)))
		for _, q := range queued {
			out = append(out, executeAndLog(q)...)
		}
		return out
	case "WATCH":
		if cs.inMulti {
			return errResp("WATCH inside MULTI is not allowed")
		}
		if cs.watching == nil {
			cs.watching = map[string]int64{}
		}
		mu.Lock()
		for _, k := range args[1:] {
			cs.watching[k] = versions[k]
		}
		mu.Unlock()
		return []byte("+OK\r\n")
	case "UNWATCH":
		cs.watching = nil
		return []byte("+OK\r\n")
	case "WAIT":
		return cmdWait(args)
	}
	if cs.inMulti {
		cs.queued = append(cs.queued, args)
		return []byte("+QUEUED\r\n")
	}
	return executeAndLog(args)
}

// executeAndLog runs a command and performs AOF logging + replica
// propagation for write commands.
func executeAndLog(args []string) []byte {
	resp := execute(args)
	name := strings.ToUpper(args[0])
	if config["appendonly"] == "yes" && isWriteCommand(name) {
		appendAOF(args)
	}
	if !isReplica() && isWriteCommand(name) {
		propagate(args)
	}
	return resp
}

func watchViolated(cs *clientState) bool {
	mu.Lock()
	defer mu.Unlock()
	for k, v := range cs.watching {
		if versions[k] != v {
			return true
		}
	}
	return false
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
	case "INCR":
		return cmdIncr(args)
	case "CONFIG":
		return cmdConfig(args)
	case "KEYS":
		return cmdKeys(args)
	case "INFO":
		return cmdInfo(args)
	case "REPLCONF":
		return []byte("+OK\r\n")
	case "TYPE":
		return cmdType(args)
	case "XADD":
		return cmdXAdd(args)
	case "XRANGE":
		return cmdXRange(args)
	case "XREAD":
		return cmdXRead(args)
	case "RPUSH":
		return cmdPush(args, false)
	case "LPUSH":
		return cmdPush(args, true)
	case "LRANGE":
		return cmdLRange(args)
	case "LLEN":
		return cmdLLen(args)
	case "LPOP":
		return cmdLPop(args)
	case "BLPOP":
		return cmdBLPop(args)
	case "ZADD":
		return cmdZAdd(args)
	case "ZRANK":
		return cmdZRank(args)
	case "ZRANGE":
		return cmdZRange(args)
	case "ZCARD":
		return cmdZCard(args)
	case "ZSCORE":
		return cmdZScore(args)
	case "ZREM":
		return cmdZRem(args)
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
	touch(args[1])
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

// --- replica handshake (this server as replica) ---

func replicaHandshake() {
	parts := strings.Fields(replicaOf)
	if len(parts) != 2 {
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(parts[0], parts[1]))
	if err != nil {
		return
	}
	r := bufio.NewReader(conn)
	send := func(a ...string) { conn.Write(encodeCommand(a)); readLine(r) }

	send("PING")
	send("REPLCONF", "listening-port", config["port"])
	send("REPLCONF", "capa", "psync2")
	conn.Write(encodeCommand([]string{"PSYNC", "?", "-1"}))
	readLine(r)    // +FULLRESYNC <id> <offset>
	readRDBBulk(r) // $<len>\r\n<binary> (no trailing CRLF)

	// Stream of propagated commands from the master. The replica applies
	// each silently and tracks how many command bytes it has processed.
	for {
		args, nbytes, err := readCommandSized(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		if strings.ToUpper(args[0]) == "REPLCONF" && len(args) >= 2 && strings.ToUpper(args[1]) == "GETACK" {
			conn.Write(encodeCommand([]string{"REPLCONF", "ACK", strconv.Itoa(replOffset)}))
		} else {
			execute(args) // apply silently, no reply to master
		}
		replOffset += nbytes // count every command, GETACK included
	}
}

// readRDBBulk consumes a "$<len>\r\n<bytes>" RDB payload from the master.
func readRDBBulk(r *bufio.Reader) {
	line, err := readLine(r)
	if err != nil || len(line) == 0 || line[0] != '$' {
		return
	}
	n, _ := strconv.Atoi(line[1:])
	io.CopyN(io.Discard, r, int64(n))
}

// --- master side: connected replicas ---

type replica struct {
	conn  net.Conn
	acked int
}

var (
	replicasMu   sync.Mutex
	replicas     []*replica
	masterOffset int // bytes of commands sent down the replication stream
)

// serveReplica reads acknowledgements from a connected replica.
func serveReplica(conn net.Conn, r *bufio.Reader) {
	rep := &replica{conn: conn}
	replicasMu.Lock()
	replicas = append(replicas, rep)
	replicasMu.Unlock()

	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) >= 3 && strings.ToUpper(args[0]) == "REPLCONF" && strings.ToUpper(args[1]) == "ACK" {
			n, _ := strconv.Atoi(args[2])
			replicasMu.Lock()
			rep.acked = n
			replicasMu.Unlock()
		}
	}
}

// propagate sends a write command to every connected replica.
func propagate(args []string) {
	buf := encodeCommand(args)
	replicasMu.Lock()
	masterOffset += len(buf)
	for _, rep := range replicas {
		rep.conn.Write(buf)
	}
	replicasMu.Unlock()
}

func cmdWait(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'wait'")
	}
	need, _ := strconv.Atoi(args[1])
	timeoutMs, _ := strconv.Atoi(args[2])

	replicasMu.Lock()
	target := masterOffset
	total := len(replicas)
	replicasMu.Unlock()

	// No writes yet: every connected replica is trivially up to date.
	if target == 0 {
		return []byte(fmt.Sprintf(":%d\r\n", total))
	}

	getack := encodeCommand([]string{"REPLCONF", "GETACK", "*"})
	replicasMu.Lock()
	for _, rep := range replicas {
		rep.conn.Write(getack)
	}
	masterOffset += len(getack)
	replicasMu.Unlock()

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		replicasMu.Lock()
		acked := 0
		for _, rep := range replicas {
			if rep.acked >= target {
				acked++
			}
		}
		replicasMu.Unlock()
		if acked >= need || time.Now().After(deadline) {
			return []byte(fmt.Sprintf(":%d\r\n", acked))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readCommandSized behaves like readCommand but also reports the number
// of raw bytes the RESP array occupied on the wire.
func readCommandSized(r *bufio.Reader) ([]string, int, error) {
	args, err := readCommand(r)
	if err != nil {
		return nil, 0, err
	}
	return args, len(encodeCommand(args)), nil
}

func emptyRDBPayload() []byte {
	const b64 = "UkVESVMwMDEx+glyZWRpcy12ZXIFNy4yLjD6CnJlZGlzLWJpdHPAQPoFY3RpbWXCbQi8ZfoIdXNlZC1tZW3CsMQQAPoIYW9mLWJhc2XAAP/wbjv+wP9aog=="
	data, _ := base64.StdEncoding.DecodeString(b64)
	return append([]byte(fmt.Sprintf("$%d\r\n", len(data))), data...)
}

func cmdInfo(args []string) []byte {
	role := "master"
	if isReplica() {
		role = "slave"
	}
	section := fmt.Sprintf("# Replication\r\nrole:%s\r\nmaster_replid:%s\r\nmaster_repl_offset:%d\r\n",
		role, replID, replOffset)
	return bulkString(section)
}

// --- streams ---

type streamEntry struct {
	ms, seq int64
	fields  []string
}

type stream struct {
	entries []streamEntry
}

var (
	streamsMu sync.Mutex
	streams   = map[string]*stream{}
)

func cmdType(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'type'")
	}
	mu.Lock()
	e, ok := store[args[1]]
	if ok && expired(e) {
		delete(store, args[1])
		ok = false
	}
	mu.Unlock()
	if ok {
		return []byte("+string\r\n")
	}
	streamsMu.Lock()
	_, isStream := streams[args[1]]
	streamsMu.Unlock()
	if isStream {
		return []byte("+stream\r\n")
	}
	return []byte("+none\r\n")
}

func cmdXAdd(args []string) []byte {
	if len(args) < 5 {
		return errResp("wrong number of arguments for 'xadd'")
	}
	key, rawID := args[1], args[2]
	streamsMu.Lock()
	defer streamsMu.Unlock()
	s := streams[key]
	if s == nil {
		s = &stream{}
	}

	ms, seq, errMsg := resolveXAddID(s, rawID)
	if errMsg != "" {
		return []byte("-ERR " + errMsg + "\r\n")
	}
	s.entries = append(s.entries, streamEntry{ms: ms, seq: seq, fields: append([]string{}, args[3:]...)})
	streams[key] = s
	return bulkString(fmt.Sprintf("%d-%d", ms, seq))
}

// resolveXAddID computes the (ms,seq) for an XADD id spec and validates
// it against the stream's current top entry.
func resolveXAddID(s *stream, raw string) (int64, int64, string) {
	var lastMs, lastSeq int64 = -1, -1
	if len(s.entries) > 0 {
		last := s.entries[len(s.entries)-1]
		lastMs, lastSeq = last.ms, last.seq
	}

	var ms, seq int64
	switch {
	case raw == "*":
		ms = time.Now().UnixMilli()
		if ms == lastMs {
			seq = lastSeq + 1
		} else if ms < lastMs {
			ms, seq = lastMs, lastSeq+1
		} else {
			seq = 0
		}
	default:
		msPart, seqPart, hasSeq := strings.Cut(raw, "-")
		m, err := strconv.ParseInt(msPart, 10, 64)
		if err != nil {
			return 0, 0, "Invalid stream ID specified as stream command argument"
		}
		ms = m
		if !hasSeq || seqPart == "*" {
			switch {
			case ms == lastMs:
				seq = lastSeq + 1
			case ms == 0:
				seq = 1
			default:
				seq = 0
			}
		} else {
			q, err := strconv.ParseInt(seqPart, 10, 64)
			if err != nil {
				return 0, 0, "Invalid stream ID specified as stream command argument"
			}
			seq = q
		}
	}

	if ms == 0 && seq == 0 {
		return 0, 0, "The ID specified in XADD must be greater than 0-0"
	}
	if ms < lastMs || (ms == lastMs && seq <= lastSeq) {
		return 0, 0, "The ID specified in XADD is equal or smaller than the target stream top item"
	}
	return ms, seq, ""
}

func cmdXRange(args []string) []byte {
	if len(args) < 4 {
		return errResp("wrong number of arguments for 'xrange'")
	}
	key := args[1]
	startMs, startSeq := parseRangeID(args[2], false)
	endMs, endSeq := parseRangeID(args[3], true)

	streamsMu.Lock()
	s := streams[key]
	streamsMu.Unlock()
	if s == nil {
		return []byte("*0\r\n")
	}
	var out [][]byte
	for _, e := range s.entries {
		if geID(e.ms, e.seq, startMs, startSeq) && leID(e.ms, e.seq, endMs, endSeq) {
			out = append(out, encodeEntry(e))
		}
	}
	return arrayOf(out...)
}

// parseRangeID parses an XRANGE bound. "-"/"+" become the min/max ID; a
// bare ms defaults its sequence to 0 (start) or max (end).
func parseRangeID(raw string, isEnd bool) (int64, int64) {
	switch raw {
	case "-":
		return 0, 0
	case "+":
		return math.MaxInt64, math.MaxInt64
	}
	msPart, seqPart, hasSeq := strings.Cut(raw, "-")
	ms, _ := strconv.ParseInt(msPart, 10, 64)
	if !hasSeq {
		if isEnd {
			return ms, math.MaxInt64
		}
		return ms, 0
	}
	seq, _ := strconv.ParseInt(seqPart, 10, 64)
	return ms, seq
}

func cmdXRead(args []string) []byte {
	// Parse optional BLOCK <ms>, then STREAMS key... id...
	i := 1
	blocking := false
	var blockMs int64 = -1
	if i < len(args) && strings.ToUpper(args[i]) == "BLOCK" {
		blocking = true
		blockMs, _ = strconv.ParseInt(args[i+1], 10, 64)
		i += 2
	}
	if i >= len(args) || strings.ToUpper(args[i]) != "STREAMS" {
		return errResp("syntax error")
	}
	i++
	rest := args[i:]
	if len(rest)%2 != 0 {
		return errResp("Unbalanced XREAD list of streams")
	}
	n := len(rest) / 2
	keys := rest[:n]
	ids := rest[n:]

	// Resolve starting IDs (handling "$" = current last entry).
	startMs := make([]int64, n)
	startSeq := make([]int64, n)
	streamsMu.Lock()
	for j := 0; j < n; j++ {
		if ids[j] == "$" {
			if s := streams[keys[j]]; s != nil && len(s.entries) > 0 {
				last := s.entries[len(s.entries)-1]
				startMs[j], startSeq[j] = last.ms, last.seq
			}
		} else {
			m, sq := parseRangeID(ids[j], false)
			startMs[j], startSeq[j] = m, sq
		}
	}
	streamsMu.Unlock()

	deadline := time.Now().Add(time.Duration(blockMs) * time.Millisecond)
	for {
		result := collectXRead(keys, startMs, startSeq)
		if result != nil {
			return result
		}
		if !blocking {
			return nullArray()
		}
		if blockMs > 0 && time.Now().After(deadline) {
			return nullArray()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// collectXRead returns the XREAD reply for streams with new entries, or
// nil when none have any.
func collectXRead(keys []string, startMs, startSeq []int64) []byte {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	var blocks [][]byte
	for j, key := range keys {
		s := streams[key]
		if s == nil {
			continue
		}
		var entries [][]byte
		for _, e := range s.entries {
			if gtID(e.ms, e.seq, startMs[j], startSeq[j]) {
				entries = append(entries, encodeEntry(e))
			}
		}
		if len(entries) > 0 {
			blocks = append(blocks, arrayOf(bulkString(key), arrayOf(entries...)))
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return arrayOf(blocks...)
}

func encodeEntry(e streamEntry) []byte {
	var fv [][]byte
	for _, f := range e.fields {
		fv = append(fv, bulkString(f))
	}
	return arrayOf(bulkString(fmt.Sprintf("%d-%d", e.ms, e.seq)), arrayOf(fv...))
}

// stream ID comparisons
func gtID(am, as, bm, bs int64) bool { return am > bm || (am == bm && as > bs) }
func geID(am, as, bm, bs int64) bool { return am > bm || (am == bm && as >= bs) }
func leID(am, as, bm, bs int64) bool { return am < bm || (am == bm && as <= bs) }

func nullArray() []byte {
	return []byte("*-1\r\n")
}

func cmdIncr(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'incr'")
	}
	mu.Lock()
	defer mu.Unlock()
	e, ok := store[args[1]]
	if ok && expired(e) {
		delete(store, args[1])
		ok = false
	}
	var n int
	if ok {
		var err error
		n, err = strconv.Atoi(e.value)
		if err != nil {
			return errResp("value is not an integer or out of range")
		}
	}
	n++
	store[args[1]] = entry{value: strconv.Itoa(n), expireAt: e.expireAt}
	touch(args[1])
	return []byte(fmt.Sprintf(":%d\r\n", n))
}

// --- lists ---

var (
	listsMu sync.Mutex
	lists   = map[string][]string{}
)

func cmdPush(args []string, left bool) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'push'")
	}
	key := args[1]
	listsMu.Lock()
	l := lists[key]
	for _, v := range args[2:] {
		if left {
			l = append([]string{v}, l...)
		} else {
			l = append(l, v)
		}
	}
	lists[key] = l
	n := len(l)
	listsMu.Unlock()
	return []byte(fmt.Sprintf(":%d\r\n", n))
}

func cmdLRange(args []string) []byte {
	if len(args) < 4 {
		return errResp("wrong number of arguments for 'lrange'")
	}
	start, _ := strconv.Atoi(args[2])
	stop, _ := strconv.Atoi(args[3])
	listsMu.Lock()
	l := lists[args[1]]
	listsMu.Unlock()

	n := len(l)
	start = clampIndex(start, n)
	stop = clampIndex(stop, n)
	if start > stop || n == 0 {
		return []byte("*0\r\n")
	}
	var out [][]byte
	for i := start; i <= stop; i++ {
		out = append(out, bulkString(l[i]))
	}
	return arrayOf(out...)
}

// clampIndex resolves a (possibly negative) LRANGE index into [0, n-1].
func clampIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	if i < 0 {
		i = 0
	}
	if i > n-1 {
		i = n - 1
	}
	return i
}

func cmdLLen(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'llen'")
	}
	listsMu.Lock()
	n := len(lists[args[1]])
	listsMu.Unlock()
	return []byte(fmt.Sprintf(":%d\r\n", n))
}

func cmdLPop(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'lpop'")
	}
	count := 1
	hasCount := len(args) >= 3
	if hasCount {
		count, _ = strconv.Atoi(args[2])
	}
	listsMu.Lock()
	defer listsMu.Unlock()
	l := lists[args[1]]
	if len(l) == 0 {
		if hasCount {
			return []byte("*0\r\n")
		}
		return nullBulk()
	}
	if count > len(l) {
		count = len(l)
	}
	popped := l[:count]
	lists[args[1]] = l[count:]
	if !hasCount {
		return bulkString(popped[0])
	}
	var out [][]byte
	for _, v := range popped {
		out = append(out, bulkString(v))
	}
	return arrayOf(out...)
}

func cmdBLPop(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'blpop'")
	}
	keys := args[1 : len(args)-1]
	timeout, _ := strconv.ParseFloat(args[len(args)-1], 64)
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))

	for {
		listsMu.Lock()
		for _, key := range keys {
			if l := lists[key]; len(l) > 0 {
				v := l[0]
				lists[key] = l[1:]
				listsMu.Unlock()
				return arrayOf(bulkString(key), bulkString(v))
			}
		}
		listsMu.Unlock()
		if timeout > 0 && time.Now().After(deadline) {
			return nullArray()
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// --- pub/sub ---

var (
	pubsubMu    sync.Mutex
	subscribers = map[string]map[*clientState]bool{} // channel -> clients
)

// commands permitted while a connection is in subscribed mode.
var subModeAllowed = map[string]bool{
	"SUBSCRIBE": true, "UNSUBSCRIBE": true, "PSUBSCRIBE": true,
	"PUNSUBSCRIBE": true, "PING": true, "QUIT": true, "RESET": true,
}

func cmdSubscribe(cs *clientState, args []string) []byte {
	if cs.subs == nil {
		cs.subs = map[string]bool{}
	}
	var out []byte
	for _, ch := range args[1:] {
		if !cs.subs[ch] {
			cs.subs[ch] = true
			pubsubMu.Lock()
			if subscribers[ch] == nil {
				subscribers[ch] = map[*clientState]bool{}
			}
			subscribers[ch][cs] = true
			pubsubMu.Unlock()
		}
		out = append(out, arrayOf(bulkString("subscribe"), bulkString(ch),
			[]byte(fmt.Sprintf(":%d\r\n", len(cs.subs))))...)
	}
	return out
}

func cmdUnsubscribe(cs *clientState, args []string) []byte {
	chans := args[1:]
	if len(chans) == 0 {
		for ch := range cs.subs {
			chans = append(chans, ch)
		}
	}
	var out []byte
	for _, ch := range chans {
		if cs.subs[ch] {
			delete(cs.subs, ch)
			pubsubMu.Lock()
			delete(subscribers[ch], cs)
			pubsubMu.Unlock()
		}
		out = append(out, arrayOf(bulkString("unsubscribe"), bulkString(ch),
			[]byte(fmt.Sprintf(":%d\r\n", len(cs.subs))))...)
	}
	return out
}

func cmdPublish(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'publish'")
	}
	channel, msg := args[1], args[2]
	pubsubMu.Lock()
	targets := make([]*clientState, 0, len(subscribers[channel]))
	for c := range subscribers[channel] {
		targets = append(targets, c)
	}
	pubsubMu.Unlock()

	payload := arrayOf(bulkString("message"), bulkString(channel), bulkString(msg))
	for _, c := range targets {
		c.write(payload)
	}
	return []byte(fmt.Sprintf(":%d\r\n", len(targets)))
}

func unsubscribeAll(cs *clientState) {
	pubsubMu.Lock()
	for ch := range cs.subs {
		delete(subscribers[ch], cs)
	}
	pubsubMu.Unlock()
}

// handleCommand dispatches one client command, honoring transaction and

// --- sorted sets ---

var (
	zsetsMu sync.Mutex
	zsets   = map[string]map[string]float64{}
)

// sortedMembers returns a zset's members ordered by score, ties broken
// lexicographically. Caller must hold zsetsMu.
func sortedMembers(key string) []string {
	z := zsets[key]
	members := make([]string, 0, len(z))
	for m := range z {
		members = append(members, m)
	}
	sort.Slice(members, func(i, j int) bool {
		if z[members[i]] != z[members[j]] {
			return z[members[i]] < z[members[j]]
		}
		return members[i] < members[j]
	})
	return members
}

func cmdZAdd(args []string) []byte {
	if len(args) < 4 {
		return errResp("wrong number of arguments for 'zadd'")
	}
	key := args[1]
	zsetsMu.Lock()
	defer zsetsMu.Unlock()
	if zsets[key] == nil {
		zsets[key] = map[string]float64{}
	}
	added := 0
	for i := 2; i+1 < len(args); i += 2 {
		score, err := strconv.ParseFloat(args[i], 64)
		if err != nil {
			return errResp("value is not a valid float")
		}
		member := args[i+1]
		if _, ok := zsets[key][member]; !ok {
			added++
		}
		zsets[key][member] = score
	}
	return []byte(fmt.Sprintf(":%d\r\n", added))
}

func cmdZRank(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'zrank'")
	}
	zsetsMu.Lock()
	defer zsetsMu.Unlock()
	if _, ok := zsets[args[1]][args[2]]; !ok {
		return nullBulk()
	}
	for i, m := range sortedMembers(args[1]) {
		if m == args[2] {
			return []byte(fmt.Sprintf(":%d\r\n", i))
		}
	}
	return nullBulk()
}

func cmdZRange(args []string) []byte {
	if len(args) < 4 {
		return errResp("wrong number of arguments for 'zrange'")
	}
	start, _ := strconv.Atoi(args[2])
	stop, _ := strconv.Atoi(args[3])
	zsetsMu.Lock()
	members := sortedMembers(args[1])
	zsetsMu.Unlock()

	n := len(members)
	if n == 0 {
		return []byte("*0\r\n")
	}
	start = clampIndex(start, n)
	stop = clampIndex(stop, n)
	if start > stop {
		return []byte("*0\r\n")
	}
	var out [][]byte
	for i := start; i <= stop; i++ {
		out = append(out, bulkString(members[i]))
	}
	return arrayOf(out...)
}

func cmdZCard(args []string) []byte {
	if len(args) < 2 {
		return errResp("wrong number of arguments for 'zcard'")
	}
	zsetsMu.Lock()
	n := len(zsets[args[1]])
	zsetsMu.Unlock()
	return []byte(fmt.Sprintf(":%d\r\n", n))
}

func cmdZScore(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'zscore'")
	}
	zsetsMu.Lock()
	score, ok := zsets[args[1]][args[2]]
	zsetsMu.Unlock()
	if !ok {
		return nullBulk()
	}
	return bulkString(formatScore(score))
}

func cmdZRem(args []string) []byte {
	if len(args) < 3 {
		return errResp("wrong number of arguments for 'zrem'")
	}
	zsetsMu.Lock()
	defer zsetsMu.Unlock()
	removed := 0
	for _, m := range args[2:] {
		if _, ok := zsets[args[1]][m]; ok {
			delete(zsets[args[1]], m)
			removed++
		}
	}
	return []byte(fmt.Sprintf(":%d\r\n", removed))
}

func formatScore(f float64) string {
	// 'f' with -1 keeps integer geohash scores integral and small
	// decimal scores minimal, both without scientific notation.
	return strconv.FormatFloat(f, 'f', -1, 64)
}
