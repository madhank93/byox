package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math"
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

	// versions counts modifications per key. A version beats storing a copy
	// of the value: it is cheap, and it still catches a write that set the
	// key back to what it was. Bumped under mu, alongside the mutation it
	// describes.
	versions = map[string]uint64{}
)

func bumpVersion(key string) {
	versions[key]++
}

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

// resolveDollarIDs replaces each "$" id with the stream's current last id, or
// 0-0 when the stream is empty.
func resolveDollarIDs(args []string) []string {
	if len(args) < 4 || !strings.EqualFold(args[1], "streams") {
		return args
	}
	out := append([]string(nil), args...)
	rest := out[2:]
	half := len(rest) / 2
	keys, ids := rest[:half], rest[half:]

	mu.Lock()
	defer mu.Unlock()
	for i, id := range ids {
		if id != "$" {
			continue
		}
		entries := streams[keys[i]]
		if n := len(entries); n > 0 {
			ids[i] = entries[n-1].id()
		} else {
			ids[i] = "0-0"
		}
	}
	return out
}

// tryXRead performs one non-blocking XREAD pass, reporting whether anything
// was found. The blocking form is this check on a timer.
func tryXRead(args []string) ([]byte, bool) {
	if len(args) < 4 || !strings.EqualFold(args[1], "streams") {
		return nil, false
	}
	rest := args[2:]
	half := len(rest) / 2
	keys, ids := rest[:half], rest[half:]

	var results [][]byte
	mu.Lock()
	for i, key := range keys {
		ms, seq, ok := parseRangeID(ids[i], true)
		if !ok {
			continue
		}
		if parts := readStreamAfter(streams[key], ms, seq); len(parts) > 0 {
			results = append(results, arrayOf(bulkString(key), arrayOf(parts...)))
		}
	}
	mu.Unlock()
	if len(results) == 0 {
		return nil, false
	}
	return arrayOf(results...), true
}

// readStreamAfter returns the entries strictly after the given id. XREAD's
// bound is exclusive — a consumer resuming from the last id it saw must not
// receive it again.
func readStreamAfter(entries []streamEntry, ms, seq uint64) [][]byte {
	var parts [][]byte
	for _, e := range entries {
		if less(ms, seq, e.ms, e.seq) {
			parts = append(parts, encodeStreamEntry(e))
		}
	}
	return parts
}

// less reports whether id a sorts before id b.
func less(aMS, aSeq, bMS, bSeq uint64) bool {
	if aMS != bMS {
		return aMS < bMS
	}
	return aSeq < bSeq
}

// parseRangeID reads an XRANGE bound. A bare millisecond means sequence 0 at
// the start of a range and the largest sequence at the end.
func parseRangeID(id string, isStart bool) (uint64, uint64, bool) {
	// "-" is a sentinel for the smallest possible id. Normalising it here
	// keeps the range scan itself single-path.
	if id == "-" {
		return 0, 0, true
	}
	// "+" is its mirror: the largest possible id, fixed rather than derived
	// from the stream's current last entry, so the bound cannot shift.
	if id == "+" {
		return math.MaxUint64, math.MaxUint64, true
	}
	if ms, seq, ok := parseStreamID(id); ok {
		return ms, seq, true
	}
	ms, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if isStart {
		return ms, 0, true
	}
	return ms, math.MaxUint64, true
}

// encodeStreamEntry renders one entry as [id, [field, value, …]].
func encodeStreamEntry(e streamEntry) []byte {
	fields := make([][]byte, len(e.fields))
	for i, f := range e.fields {
		fields[i] = bulkString(f)
	}
	return arrayOf(bulkString(e.id()), arrayOf(fields...))
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

// zsets holds the sorted-set type: unique members, each with a score, ordered
// by score with ties broken lexicographically by member.
var zsets = map[string]map[string]float64{}

// lists holds the list type. Redis has no CREATE: the first write to a key
// defines its type.
var lists = map[string][]string{}

// client is one connection's own state. A transaction belongs to the
// connection that opened it, never to the server, so it lives here.
type client struct {
	w       io.Writer
	inMulti bool
	queued  [][]string

	// watched maps a key to the version this connection saw when it was
	// watched, so EXEC can tell whether anyone has since changed it.
	watched map[string]uint64

	// channels is this connection's subscription set.
	channels map[string]bool

	// writeMu serialises writes to this connection. Delivery happens from
	// the publisher's goroutine, so two publishers would otherwise interleave
	// halves of two messages into one subscriber's stream.
	writeMu sync.Mutex
}

// subscribers maps a channel to the connections listening on it. A publisher
// writes into other connections, so both this map and each connection's
// writes need guarding.
var (
	subsMu      sync.Mutex
	subscribers = map[string][]*client{}
)

// unsubscribe removes the subscription from both registries. A stale entry in
// the server map means writing to a connection that no longer expects
// messages.
func (c *client) unsubscribe(channel string) {
	delete(c.channels, channel)
	subsMu.Lock()
	defer subsMu.Unlock()
	kept := subscribers[channel][:0]
	for _, sub := range subscribers[channel] {
		if sub != c {
			kept = append(kept, sub)
		}
	}
	if len(kept) == 0 {
		delete(subscribers, channel)
		return
	}
	subscribers[channel] = kept
}

func (c *client) subscribe(channel string) {
	if c.channels == nil {
		c.channels = map[string]bool{}
	}
	c.channels[channel] = true
	subsMu.Lock()
	subscribers[channel] = append(subscribers[channel], c)
	subsMu.Unlock()
}

// resetTransaction returns the connection to its baseline. Every exit from a
// transaction goes through here, so commit and abort cannot drift apart.
// watchConflict reports whether any watched key changed since it was watched.
func (c *client) watchConflict() bool {
	mu.Lock()
	defer mu.Unlock()
	for key, seen := range c.watched {
		if versions[key] != seen {
			return true
		}
	}
	return false
}

func (c *client) resetTransaction() {
	c.inMulti = false
	c.queued = nil
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	c := &client{w: conn}
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
		dispatch(c, args)
	}
}

// dispatch handles the commands that act on the connection itself, then
// delegates the rest to execute.
// subscribedAllowed is the command set a subscribed connection keeps. Once
// messages arrive unprompted, a client can no longer match replies to
// requests, so the rest are refused.
var subscribedAllowed = map[string]bool{
	"SUBSCRIBE": true, "UNSUBSCRIBE": true, "PSUBSCRIBE": true,
	"PUNSUBSCRIBE": true, "PING": true, "QUIT": true, "RESET": true,
}

func dispatch(c *client, args []string) {
	name := strings.ToUpper(args[0])
	if len(c.channels) > 0 && !subscribedAllowed[name] {
		fmt.Fprintf(c.w, "-ERR Can't execute '%s': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context\r\n",
			strings.ToLower(args[0]))
		return
	}
	switch name {
	case "MULTI":
		c.inMulti = true
		c.w.Write([]byte("+OK\r\n"))
	case "EXEC":
		// A command's validity now depends on what came before on this
		// connection, not on its arguments alone.
		if !c.inMulti {
			c.w.Write([]byte("-ERR EXEC without MULTI\r\n"))
			return
		}
		batch := c.queued
		conflicted := c.watchConflict()
		// The watches existed to protect this transaction; carrying them
		// forward would abandon a later one for a change already accounted
		// for. Both outcomes — committed and abandoned — clear them.
		c.watched = nil
		c.resetTransaction()
		if conflicted {
			// A null array, distinct from the empty array of an untouched
			// empty transaction.
			c.w.Write([]byte("*-1\r\n"))
			return
		}

		// A failing command does not abandon the transaction: its error is
		// one element of the reply and the rest still run. Redis transactions
		// guarantee isolation and ordering, not rollback.
		var body bytes.Buffer
		for _, qargs := range batch {
			execute(&body, qargs)
		}
		// An empty array, not a null array and not +OK — RESP distinguishes
		// "no elements" from "no value".
		fmt.Fprintf(c.w, "*%d\r\n", len(batch))
		c.w.Write(body.Bytes())
	case "PING":
		// Everything arriving on a subscribed connection is an array, so a
		// client can read messages uniformly with no special case.
		if len(c.channels) > 0 {
			c.w.Write(arrayOf(bulkString("pong"), bulkString("")))
			return
		}
		execute(c.w, args)
	case "PUBLISH":
		if len(args) > 2 {
			subsMu.Lock()
			targets := append([]*client(nil), subscribers[args[1]]...)
			subsMu.Unlock()

			// Copy first, then write: the publisher may itself be one of the
			// subscribers, and a non-reentrant lock would deadlock.
			message := arrayOf(bulkString("message"), bulkString(args[1]), bulkString(args[2]))
			for _, sub := range targets {
				sub.writeMu.Lock()
				sub.w.Write(message)
				sub.writeMu.Unlock()
			}
			n := len(targets)
			// The count is the publisher's only feedback: delivery is
			// fire-and-forget, and 0 means the message is simply gone.
			fmt.Fprintf(c.w, ":%d\r\n", n)
		}
	case "SUBSCRIBE":
		for _, channel := range args[1:] {
			c.subscribe(channel)
			// The count is per connection and grows as subscriptions
			// accumulate — a set, so subscribing twice does not count twice.
			c.w.Write(arrayOf(bulkString("subscribe"), bulkString(channel),
				[]byte(fmt.Sprintf(":%d\r\n", len(c.channels)))))
		}
	case "UNSUBSCRIBE":
		for _, channel := range args[1:] {
			c.unsubscribe(channel)
			c.w.Write(arrayOf(bulkString("unsubscribe"), bulkString(channel),
				[]byte(fmt.Sprintf(":%d\r\n", len(c.channels)))))
		}
	case "WATCH":
		// Watching is what you do before deciding what to queue, so a watch
		// registered after that decision could protect nothing.
		if c.inMulti {
			c.w.Write([]byte("-ERR WATCH inside MULTI is not allowed\r\n"))
			return
		}
		// Optimistic concurrency: no lock is taken, the conflict is detected
		// at commit time instead.
		if c.watched == nil {
			c.watched = map[string]uint64{}
		}
		mu.Lock()
		// One WATCH can name several keys, and any one of them changing is
		// enough to abandon the transaction.
		for _, key := range args[1:] {
			// Absence is a state like any other: a key that does not exist
			// yet is at version 0, and creating it counts as a change.
			c.watched[key] = versions[key]
		}
		mu.Unlock()
		c.w.Write([]byte("+OK\r\n"))
	case "UNWATCH":
		// The watch set is per-connection and outlives any single MULTI
		// unless it is cleared.
		c.watched = nil
		c.w.Write([]byte("+OK\r\n"))
	case "DISCARD":
		// Abort and commit must reset exactly the same state, or the
		// connection is left as no valid sequence could leave it.
		if !c.inMulti {
			c.w.Write([]byte("-ERR DISCARD without MULTI\r\n"))
			return
		}
		c.watched = nil
		c.resetTransaction()
		c.w.Write([]byte("+OK\r\n"))
	default:
		// Inside a transaction the dispatcher stops executing and starts
		// storing. That interception point is the whole of transactions.
		if c.inMulti {
			c.queued = append(c.queued, append([]string(nil), args...))
			c.w.Write([]byte("+QUEUED\r\n"))
			return
		}
		execute(c.w, args)
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
				bumpVersion(args[1])
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
		case "INCR":
			if len(args) > 1 {
				// Read-modify-write: the whole sequence happens under one
				// lock, or two concurrent INCRs produce one increment.
				mu.Lock()
				// A missing key is the empty value of the type the command
				// needs, so a counter needs no initialisation.
				e, exists := store[args[1]]
				var n int64
				if exists {
					// Redis is dynamically typed but not permissive: the
					// command validates at execution time and refuses.
					parsed, err := strconv.ParseInt(e.value, 10, 64)
					if err != nil {
						mu.Unlock()
						conn.Write([]byte("-ERR value is not an integer or out of range\r\n"))
						return
					}
					n = parsed
				}
				n++
				e.value = strconv.FormatInt(n, 10)
				store[args[1]] = e
				bumpVersion(args[1])
				mu.Unlock()
				fmt.Fprintf(conn, ":%d\r\n", n)
			}
		case "RPUSH":
			if len(args) > 2 {
				mu.Lock()
				// Variadic: three appends in one command is one round trip
				// instead of three.
				lists[args[1]] = append(lists[args[1]], args[2:]...)
				n := len(lists[args[1]])
				bumpVersion(args[1])
				mu.Unlock()
				fmt.Fprintf(conn, ":%d\r\n", n)
			}
		case "LPUSH":
			if len(args) > 2 {
				mu.Lock()
				// Each element is pushed in turn, so the arguments end up
				// reversed at the head.
				for _, v := range args[2:] {
					lists[args[1]] = append([]string{v}, lists[args[1]]...)
				}
				n := len(lists[args[1]])
				bumpVersion(args[1])
				mu.Unlock()
				fmt.Fprintf(conn, ":%d\r\n", n)
			}
		case "BLPOP":
			if len(args) > 2 {
				key := args[1]
				// The timeout is in seconds as a float here — XREAD's BLOCK
				// took integer milliseconds. Zero still means wait forever.
				secs, err := strconv.ParseFloat(args[2], 64)
				if err != nil {
					conn.Write([]byte("-ERR timeout is not a float or out of range\r\n"))
					return
				}
				deadline := time.Time{}
				if secs > 0 {
					deadline = time.Now().Add(time.Duration(secs * float64(time.Second)))
				}
				for deadline.IsZero() || time.Now().Before(deadline) {
					// Check and pop under one lock: checking, releasing, then
					// popping is how two blocked clients receive the same
					// element.
					mu.Lock()
					list := lists[key]
					if len(list) > 0 {
						head := list[0]
						if rest := list[1:]; len(rest) == 0 {
							delete(lists, key)
						} else {
							lists[key] = rest
						}
						bumpVersion(key)
						mu.Unlock()
						conn.Write(arrayOf(bulkString(key), bulkString(head)))
						return
					}
					mu.Unlock()
					// The sleep is outside the lock: a waiter holding it
					// would deadlock the writer that would unblock it.
					time.Sleep(20 * time.Millisecond)
				}
				conn.Write([]byte("*-1\r\n"))
			}
		case "LPOP":
			if len(args) > 1 {
				mu.Lock()
				list := lists[args[1]]
				if len(list) == 0 {
					mu.Unlock()
					conn.Write([]byte("$-1\r\n"))
					return
				}
				// With a count the reply is an array; without one it is a
				// bare bulk string. "Supplied as 1" and "not supplied" are
				// different replies.
				if len(args) > 2 {
					count, err := strconv.Atoi(args[2])
					if err != nil {
						mu.Unlock()
						conn.Write([]byte("-ERR value is not an integer or out of range\r\n"))
						return
					}
					if count > len(list) {
						count = len(list)
					}
					popped := list[:count]
					if rest := list[count:]; len(rest) == 0 {
						delete(lists, args[1])
					} else {
						lists[args[1]] = rest
					}
					bumpVersion(args[1])
					mu.Unlock()
					parts := make([][]byte, 0, len(popped))
					for _, v := range popped {
						parts = append(parts, bulkString(v))
					}
					conn.Write(arrayOf(parts...))
					return
				}
				head := list[0]
				rest := list[1:]
				// A list that empties is deleted, not kept as an empty list,
				// so TYPE and EXISTS report it as missing.
				if len(rest) == 0 {
					delete(lists, args[1])
				} else {
					lists[args[1]] = rest
				}
				bumpVersion(args[1])
				mu.Unlock()
				conn.Write(bulkString(head))
			}
		case "LLEN":
			if len(args) > 1 {
				mu.Lock()
				n := len(lists[args[1]])
				mu.Unlock()
				fmt.Fprintf(conn, ":%d\r\n", n)
			}
		case "LRANGE":
			if len(args) > 3 {
				start, err1 := strconv.Atoi(args[2])
				stop, err2 := strconv.Atoi(args[3])
				if err1 != nil || err2 != nil {
					conn.Write([]byte("-ERR value is not an integer or out of range\r\n"))
					return
				}
				mu.Lock()
				list := lists[args[1]]
				mu.Unlock()

				// Both ends are inclusive, and out-of-range bounds clamp
				// rather than error — that is what lets a client page through
				// a list without knowing its length.
				// A negative index counts from the end; normalise first, then
				// the bounds logic below is unchanged.
				if start < 0 {
					start += len(list)
				}
				if stop < 0 {
					stop += len(list)
				}
				if start < 0 {
					start = 0
				}
				if stop >= len(list) {
					stop = len(list) - 1
				}
				if start > stop || start >= len(list) {
					conn.Write([]byte("*0\r\n"))
					return
				}
				parts := make([][]byte, 0, stop-start+1)
				for _, v := range list[start : stop+1] {
					parts = append(parts, bulkString(v))
				}
				conn.Write(arrayOf(parts...))
			}
		case "ZADD":
			if len(args) > 3 {
				score, err := strconv.ParseFloat(args[2], 64)
				if err != nil {
					conn.Write([]byte("-ERR value is not a valid float\r\n"))
					return
				}
				mu.Lock()
				set := zsets[args[1]]
				if set == nil {
					set = map[string]float64{}
					zsets[args[1]] = set
				}
				set[args[3]] = score
				bumpVersion(args[1])
				mu.Unlock()
				conn.Write([]byte(":1\r\n"))
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
		case "XRANGE":
			if len(args) > 3 {
				startMS, startSeq, ok1 := parseRangeID(args[2], true)
				endMS, endSeq, ok2 := parseRangeID(args[3], false)
				if !ok1 || !ok2 {
					conn.Write([]byte("-ERR Invalid stream ID specified as stream command argument\r\n"))
					return
				}
				mu.Lock()
				entries := streams[args[1]]
				parts := make([][]byte, 0, len(entries))
				for _, e := range entries {
					// Both ends are inclusive.
					if less(e.ms, e.seq, startMS, startSeq) || less(endMS, endSeq, e.ms, e.seq) {
						continue
					}
					parts = append(parts, encodeStreamEntry(e))
				}
				mu.Unlock()
				conn.Write(arrayOf(parts...))
			}
		case "XREAD":
			rest := args[1:]
			// BLOCK <ms> precedes the streams keyword when present.
			blockMS, blocking := -1, false
			if len(rest) > 2 && strings.EqualFold(rest[0], "BLOCK") {
				if ms, err := strconv.Atoi(rest[1]); err == nil {
					blockMS, blocking = ms, true
				}
				rest = rest[2:]
				args = append([]string{args[0]}, rest...)
			}
			if blocking {
				// "$" means "entries added after this call started", so it is
				// resolved once, now — re-resolving inside the wait loop would
				// silently drop anything added in between.
				args = resolveDollarIDs(args)

				// BLOCK 0 means wait indefinitely, so the deadline is only
				// computed when a timeout was actually given.
				deadline := time.Time{}
				if blockMS > 0 {
					deadline = time.Now().Add(time.Duration(blockMS) * time.Millisecond)
				}
				for deadline.IsZero() || time.Now().Before(deadline) {
					if out, ok := tryXRead(args); ok {
						conn.Write(out)
						return
					}
					// The keyspace lock is never held across the sleep: a
					// blocked reader holding it would deadlock the writer
					// that would unblock it.
					time.Sleep(20 * time.Millisecond)
				}
				if out, ok := tryXRead(args); ok {
					conn.Write(out)
					return
				}
				conn.Write([]byte("*-1\r\n"))
				return
			}
			if len(args) > 3 && strings.EqualFold(args[1], "streams") {
				// Every key comes first, then every id — so the tail splits
				// in half without knowing how many streams were asked for.
				rest := args[2:]
				half := len(rest) / 2
				keys, ids := rest[:half], rest[half:]

				var results [][]byte
				mu.Lock()
				for i, key := range keys {
					ms, seq, ok := parseRangeID(ids[i], true)
					if !ok {
						continue
					}
					// A stream with nothing new is left out of the reply
					// entirely rather than included as an empty array.
					if parts := readStreamAfter(streams[key], ms, seq); len(parts) > 0 {
						results = append(results, arrayOf(bulkString(key), arrayOf(parts...)))
					}
				}
				mu.Unlock()
				if len(results) == 0 {
					conn.Write([]byte("*-1\r\n"))
					return
				}
				conn.Write(arrayOf(results...))
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
