package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	var err error
	switch command := os.Args[1]; command {
	case "init":
		err = cmdInit()
	case "cat-file":
		err = cmdCatFile(os.Args[2:])
	case "hash-object":
		err = cmdHashObject(os.Args[2:])
	case "ls-tree":
		err = cmdLsTree(os.Args[2:])
	case "write-tree":
		err = cmdWriteTree()
	case "commit-tree":
		err = cmdCommitTree(os.Args[2:])
	case "clone":
		err = cmdClone(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func cmdInit() error {
	for _, dir := range []string{".git", ".git/objects", ".git/refs"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}
	if err := os.WriteFile(".git/HEAD", []byte("ref: refs/heads/main\n"), 0644); err != nil {
		return fmt.Errorf("writing HEAD: %w", err)
	}
	fmt.Println("Initialized git directory")
	return nil
}

// objectPath returns the .git/objects path for a 40-char hex SHA.
func objectPath(sha string) string {
	return filepath.Join(".git", "objects", sha[:2], sha[2:])
}

// readObject reads and decompresses the object with the given hex SHA,
// returning its type ("blob"/"tree"/"commit") and content (header stripped).
func readObject(sha string) (string, []byte, error) {
	data, err := os.ReadFile(objectPath(sha))
	if err != nil {
		return "", nil, fmt.Errorf("reading object: %w", err)
	}
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", nil, fmt.Errorf("zlib reader: %w", err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("zlib decompress: %w", err)
	}
	nul := bytes.IndexByte(raw, 0)
	if nul < 0 {
		return "", nil, fmt.Errorf("malformed object: no null byte")
	}
	header := string(raw[:nul])
	var typ string
	var size int
	if _, err := fmt.Sscanf(header, "%s %d", &typ, &size); err != nil {
		return "", nil, fmt.Errorf("malformed object header %q: %w", header, err)
	}
	return typ, raw[nul+1:], nil
}

// writeObject computes the SHA-1 hash of typ+content, and if write is true,
// zlib-compresses it and writes it to .git/objects.
func writeObject(typ string, content []byte, write bool) (string, error) {
	header := fmt.Sprintf("%s %d\x00", typ, len(content))
	full := append([]byte(header), content...)

	sum := sha1.Sum(full)
	sha := fmt.Sprintf("%x", sum)

	if write {
		path := objectPath(sha)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("creating object dir: %w", err)
		}
		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(full); err != nil {
			return "", fmt.Errorf("zlib compress: %w", err)
		}
		if err := w.Close(); err != nil {
			return "", fmt.Errorf("zlib close: %w", err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			return "", fmt.Errorf("writing object: %w", err)
		}
	}
	return sha, nil
}

func cmdCatFile(args []string) error {
	if len(args) < 2 || args[0] != "-p" {
		return fmt.Errorf("usage: mygit cat-file -p <blob_sha>")
	}
	_, content, err := readObject(args[1])
	if err != nil {
		return err
	}
	fmt.Print(string(content))
	return nil
}

func cmdHashObject(args []string) error {
	write := false
	var path string
	for _, a := range args {
		if a == "-w" {
			write = true
		} else {
			path = a
		}
	}
	if path == "" {
		return fmt.Errorf("usage: mygit hash-object [-w] <file>")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	sha, err := writeObject("blob", content, write)
	if err != nil {
		return err
	}
	fmt.Println(sha)
	return nil
}

// treeEntry is a single parsed entry from a tree object.
type treeEntry struct {
	mode string
	name string
	sha  string // hex
}

func parseTree(content []byte) ([]treeEntry, error) {
	var entries []treeEntry
	for len(content) > 0 {
		sp := bytes.IndexByte(content, ' ')
		if sp < 0 {
			return nil, fmt.Errorf("malformed tree entry: no space")
		}
		mode := string(content[:sp])
		rest := content[sp+1:]
		nul := bytes.IndexByte(rest, 0)
		if nul < 0 {
			return nil, fmt.Errorf("malformed tree entry: no null byte")
		}
		name := string(rest[:nul])
		if len(rest) < nul+1+20 {
			return nil, fmt.Errorf("malformed tree entry: short sha")
		}
		sha := fmt.Sprintf("%x", rest[nul+1:nul+21])
		entries = append(entries, treeEntry{mode: mode, name: name, sha: sha})
		content = rest[nul+21:]
	}
	return entries, nil
}

func cmdLsTree(args []string) error {
	nameOnly := false
	var sha string
	for _, a := range args {
		if a == "--name-only" {
			nameOnly = true
		} else {
			sha = a
		}
	}
	if sha == "" {
		return fmt.Errorf("usage: mygit ls-tree [--name-only] <tree_sha>")
	}
	typ, content, err := readObject(sha)
	if err != nil {
		return err
	}
	if typ != "tree" {
		return fmt.Errorf("object %s is not a tree", sha)
	}
	entries, err := parseTree(content)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if nameOnly {
			fmt.Println(e.name)
			continue
		}
		objType := "blob"
		if e.mode == "40000" {
			objType = "tree"
		}
		fmt.Printf("%06s %s %s\t%s\n", e.mode, objType, e.sha, e.name)
	}
	return nil
}

func cmdWriteTree() error {
	sha, err := writeTreeForDir(".")
	if err != nil {
		return err
	}
	fmt.Println(sha)
	return nil
}

// writeTreeForDir recursively writes a tree object for dir and returns its SHA.
func writeTreeForDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading dir %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var buf bytes.Buffer
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", path, err)
		}
		var mode string
		var sha string
		if e.IsDir() {
			mode = "40000"
			sha, err = writeTreeForDir(path)
			if err != nil {
				return "", err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", fmt.Errorf("readlink %s: %w", path, err)
			}
			mode = "120000"
			sha, err = writeObject("blob", []byte(target), true)
			if err != nil {
				return "", err
			}
		} else {
			content, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("reading %s: %w", path, err)
			}
			if info.Mode()&0111 != 0 {
				mode = "100755"
			} else {
				mode = "100644"
			}
			sha, err = writeObject("blob", content, true)
			if err != nil {
				return "", err
			}
		}
		shaBytes, err := hexToBytes(sha)
		if err != nil {
			return "", err
		}
		buf.WriteString(mode)
		buf.WriteByte(' ')
		buf.WriteString(e.Name())
		buf.WriteByte(0)
		buf.Write(shaBytes)
	}
	return writeObject("tree", buf.Bytes(), true)
}

func hexToBytes(s string) ([]byte, error) {
	if len(s) != 40 {
		return nil, fmt.Errorf("invalid sha %q", s)
	}
	out := make([]byte, 20)
	for i := 0; i < 20; i++ {
		b, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid sha %q: %w", s, err)
		}
		out[i] = byte(b)
	}
	return out, nil
}

func cmdCommitTree(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mygit commit-tree <tree_sha> [-p <parent_sha>] -m <message>")
	}
	treeSha := args[0]
	var parentSha, message string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-p":
			i++
			if i < len(args) {
				parentSha = args[i]
			}
		case "-m":
			i++
			if i < len(args) {
				message = args[i]
			}
		}
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "tree %s\n", treeSha)
	if parentSha != "" {
		fmt.Fprintf(&buf, "parent %s\n", parentSha)
	}
	const author = "John Doe <john@example.com> 1234567890 +0000"
	fmt.Fprintf(&buf, "author %s\n", author)
	fmt.Fprintf(&buf, "committer %s\n", author)
	fmt.Fprintf(&buf, "\n%s\n", message)

	sha, err := writeObject("commit", buf.Bytes(), true)
	if err != nil {
		return err
	}
	fmt.Println(sha)
	return nil
}

// ---- clone (Git Smart HTTP protocol v0) ----

func cmdClone(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: mygit clone <url> <dir>")
	}
	url, dir := strings.TrimSuffix(args[0], "/"), args[1]

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("chdir %s: %w", dir, err)
	}
	if err := cmdInit(); err != nil {
		return err
	}

	headSha, branch, err := discoverRefs(url)
	if err != nil {
		return fmt.Errorf("discovering refs: %w", err)
	}

	packBytes, err := fetchPack(url, headSha)
	if err != nil {
		return fmt.Errorf("fetching pack: %w", err)
	}

	entries, err := parsePack(packBytes)
	if err != nil {
		return fmt.Errorf("parsing pack: %w", err)
	}
	if err := writePackObjects(entries); err != nil {
		return fmt.Errorf("writing objects: %w", err)
	}

	if err := os.MkdirAll(".git/refs/heads", 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(".git/refs/heads", branch), []byte(headSha+"\n"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(".git/HEAD", []byte("ref: refs/heads/"+branch+"\n"), 0644); err != nil {
		return err
	}

	typ, commitContent, err := readObject(headSha)
	if err != nil {
		return fmt.Errorf("reading cloned commit: %w", err)
	}
	if typ != "commit" {
		return fmt.Errorf("HEAD %s is not a commit", headSha)
	}
	treeSha, err := commitTreeSha(commitContent)
	if err != nil {
		return err
	}
	if err := checkoutTree(treeSha, "."); err != nil {
		return fmt.Errorf("checking out tree: %w", err)
	}
	return nil
}

func commitTreeSha(commitContent []byte) (string, error) {
	for _, line := range strings.Split(string(commitContent), "\n") {
		if strings.HasPrefix(line, "tree ") {
			return strings.TrimPrefix(line, "tree "), nil
		}
	}
	return "", fmt.Errorf("commit object has no tree line")
}

// checkoutTree recursively materializes the tree object at sha into dir.
func checkoutTree(sha, dir string) error {
	typ, content, err := readObject(sha)
	if err != nil {
		return err
	}
	if typ != "tree" {
		return fmt.Errorf("object %s is not a tree", sha)
	}
	entries, err := parseTree(content)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.name)
		if e.mode == "40000" {
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
			if err := checkoutTree(e.sha, path); err != nil {
				return err
			}
			continue
		}
		typ, blob, err := readObject(e.sha)
		if err != nil {
			return err
		}
		if typ != "blob" {
			return fmt.Errorf("object %s is not a blob", e.sha)
		}
		mode := os.FileMode(0644)
		if e.mode == "100755" {
			mode = 0755
		}
		if e.mode == "120000" {
			if err := os.Symlink(string(blob), path); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(path, blob, mode); err != nil {
			return err
		}
	}
	return nil
}

// pkt-line helpers.

func readPktLine(r *bytes.Reader) (data []byte, flush bool, err error) {
	var lenHex [4]byte
	if _, err := io.ReadFull(r, lenHex[:]); err != nil {
		return nil, false, err
	}
	var length int
	if _, err := fmt.Sscanf(string(lenHex[:]), "%04x", &length); err != nil {
		return nil, false, fmt.Errorf("bad pkt-line length %q: %w", lenHex, err)
	}
	if length == 0 {
		return nil, true, nil
	}
	buf := make([]byte, length-4)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, false, err
	}
	return buf, false, nil
}

func writePktLine(w *bytes.Buffer, s string) {
	fmt.Fprintf(w, "%04x%s", len(s)+4, s)
}

func writeFlushPkt(w *bytes.Buffer) {
	w.WriteString("0000")
}

// discoverRefs performs the initial GET .../info/refs?service=git-upload-pack
// and returns the SHA and branch name that HEAD points at.
func discoverRefs(url string) (sha, branch string, err error) {
	resp, err := http.Get(url + "/info/refs?service=git-upload-pack")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GET info/refs: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	r := bytes.NewReader(body)

	// "# service=git-upload-pack\n"
	if _, _, err := readPktLine(r); err != nil {
		return "", "", err
	}
	// flush-pkt
	if _, _, err := readPktLine(r); err != nil {
		return "", "", err
	}

	first, flush, err := readPktLine(r)
	if err != nil {
		return "", "", err
	}
	if flush {
		return "", "", fmt.Errorf("no refs advertised")
	}
	nul := bytes.IndexByte(first, 0)
	if nul < 0 {
		return "", "", fmt.Errorf("malformed ref advertisement line: %q", first)
	}
	refLine := string(first[:nul])
	caps := string(first[nul+1:])
	parts := strings.SplitN(refLine, " ", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed ref line: %q", refLine)
	}
	sha = parts[0]
	branch = "main"
	for _, cap := range strings.Fields(caps) {
		if strings.HasPrefix(cap, "symref=HEAD:refs/heads/") {
			branch = strings.TrimPrefix(cap, "symref=HEAD:refs/heads/")
		}
	}
	return sha, branch, nil
}

// fetchPack negotiates a want/done exchange over POST .../git-upload-pack and
// returns the raw packfile bytes (demultiplexed from the sideband channel).
func fetchPack(url, sha string) ([]byte, error) {
	var reqBody bytes.Buffer
	writePktLine(&reqBody, fmt.Sprintf("want %s side-band-64k ofs-delta\n", sha))
	writeFlushPkt(&reqBody)
	writePktLine(&reqBody, "done\n")

	resp, err := http.Post(url+"/git-upload-pack", "application/x-git-upload-pack-request", &reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("POST git-upload-pack: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return demuxSideband(body)
}

// demuxSideband strips the initial NAK/ACK line(s) and demultiplexes the
// sideband-64k stream, returning only the pack-data channel's bytes.
func demuxSideband(body []byte) ([]byte, error) {
	r := bytes.NewReader(body)
	for {
		data, flush, err := readPktLine(r)
		if err != nil {
			return nil, err
		}
		if flush {
			continue
		}
		if bytes.HasPrefix(data, []byte("NAK")) {
			break
		}
		if bytes.HasPrefix(data, []byte("ACK")) {
			continue
		}
		return nil, fmt.Errorf("unexpected line before sideband: %q", data)
	}

	var pack bytes.Buffer
	for {
		data, flush, err := readPktLine(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if flush {
			continue
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case 1:
			pack.Write(data[1:])
		case 2:
			fmt.Fprintf(os.Stderr, "remote: %s", data[1:])
		case 3:
			return nil, fmt.Errorf("remote error: %s", data[1:])
		default:
			pack.Write(data)
		}
	}
	return pack.Bytes(), nil
}

// ---- packfile parsing + delta resolution ----

const (
	objCommit   = 1
	objTree     = 2
	objBlob     = 3
	objTag      = 4
	objOfsDelta = 6
	objRefDelta = 7
)

type packEntry struct {
	offset       int64
	typ          int
	data         []byte // raw decompressed bytes: content for non-delta, delta-data for delta
	ofsDeltaBase int64  // absolute offset of base object, valid if typ==objOfsDelta
	refDeltaBase [20]byte
}

func parsePack(pack []byte) ([]packEntry, error) {
	if len(pack) < 12 || string(pack[:4]) != "PACK" {
		return nil, fmt.Errorf("not a valid packfile")
	}
	numObjects := binary.BigEndian.Uint32(pack[8:12])

	r := bytes.NewReader(pack)
	if _, err := r.Seek(12, io.SeekStart); err != nil {
		return nil, err
	}

	entries := make([]packEntry, 0, numObjects)
	for i := uint32(0); i < numObjects; i++ {
		startOffset := int64(len(pack)) - int64(r.Len())

		c, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		typ := int((c >> 4) & 0x7)
		size := uint64(c & 0x0f)
		shift := uint(4)
		for c&0x80 != 0 {
			c, err = r.ReadByte()
			if err != nil {
				return nil, err
			}
			size |= uint64(c&0x7f) << shift
			shift += 7
		}

		e := packEntry{offset: startOffset, typ: typ}
		switch typ {
		case objOfsDelta:
			c, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			offset := int64(c & 0x7f)
			for c&0x80 != 0 {
				c, err = r.ReadByte()
				if err != nil {
					return nil, err
				}
				offset += 1
				offset = (offset << 7) | int64(c&0x7f)
			}
			e.ofsDeltaBase = startOffset - offset
		case objRefDelta:
			var sha [20]byte
			if _, err := io.ReadFull(r, sha[:]); err != nil {
				return nil, err
			}
			e.refDeltaBase = sha
		}

		zr, err := zlib.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("object %d (type %d, offset %d): %w", i, typ, startOffset, err)
		}
		content, err := io.ReadAll(zr)
		zr.Close()
		if err != nil {
			return nil, fmt.Errorf("object %d: zlib decompress: %w", i, err)
		}
		if uint64(len(content)) != size {
			return nil, fmt.Errorf("object %d: size mismatch: header=%d actual=%d", i, size, len(content))
		}
		e.data = content
		entries = append(entries, e)
	}
	return entries, nil
}

func applyDelta(base, delta []byte) ([]byte, error) {
	pos := 0
	readVarInt := func() (int, error) {
		result, shift := 0, 0
		for {
			if pos >= len(delta) {
				return 0, fmt.Errorf("delta truncated")
			}
			b := delta[pos]
			pos++
			result |= int(b&0x7f) << shift
			shift += 7
			if b&0x80 == 0 {
				break
			}
		}
		return result, nil
	}
	baseSize, err := readVarInt()
	if err != nil {
		return nil, err
	}
	if baseSize != len(base) {
		return nil, fmt.Errorf("delta base size mismatch: expected %d got %d", baseSize, len(base))
	}
	resultSize, err := readVarInt()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, resultSize)
	for pos < len(delta) {
		cmd := delta[pos]
		pos++
		if cmd&0x80 != 0 {
			var offset, size int
			if cmd&0x01 != 0 {
				offset |= int(delta[pos])
				pos++
			}
			if cmd&0x02 != 0 {
				offset |= int(delta[pos]) << 8
				pos++
			}
			if cmd&0x04 != 0 {
				offset |= int(delta[pos]) << 16
				pos++
			}
			if cmd&0x08 != 0 {
				offset |= int(delta[pos]) << 24
				pos++
			}
			if cmd&0x10 != 0 {
				size |= int(delta[pos])
				pos++
			}
			if cmd&0x20 != 0 {
				size |= int(delta[pos]) << 8
				pos++
			}
			if cmd&0x40 != 0 {
				size |= int(delta[pos]) << 16
				pos++
			}
			if size == 0 {
				size = 0x10000
			}
			if offset+size > len(base) {
				return nil, fmt.Errorf("copy out of range: offset=%d size=%d baseLen=%d", offset, size, len(base))
			}
			out = append(out, base[offset:offset+size]...)
		} else if cmd != 0 {
			n := int(cmd)
			out = append(out, delta[pos:pos+n]...)
			pos += n
		} else {
			return nil, fmt.Errorf("invalid delta opcode 0")
		}
	}
	if len(out) != resultSize {
		return nil, fmt.Errorf("delta result size mismatch: expected %d got %d", resultSize, len(out))
	}
	return out, nil
}

func packObjTypeName(t int) string {
	switch t {
	case objCommit:
		return "commit"
	case objTree:
		return "tree"
	case objBlob:
		return "blob"
	case objTag:
		return "tag"
	}
	return "unknown"
}

// writePackObjects resolves every entry (applying deltas as needed) and
// writes the results as loose objects in .git/objects.
func writePackObjects(entries []packEntry) error {
	type resolvedObj struct {
		typ     int
		content []byte
	}
	memo := make(map[int64]resolvedObj, len(entries))
	shaToOffset := make(map[string]int64, len(entries))

	for _, e := range entries {
		if e.typ != objOfsDelta && e.typ != objRefDelta {
			sha, err := writeObject(packObjTypeName(e.typ), e.data, true)
			if err != nil {
				return err
			}
			memo[e.offset] = resolvedObj{typ: e.typ, content: e.data}
			shaToOffset[sha] = e.offset
		}
	}

	for progressed := true; progressed; {
		progressed = false
		for _, e := range entries {
			if _, done := memo[e.offset]; done {
				continue
			}
			var baseOffset int64
			var haveBase bool
			if e.typ == objOfsDelta {
				baseOffset, haveBase = e.ofsDeltaBase, true
			} else if e.typ == objRefDelta {
				baseOffset, haveBase = shaToOffset[fmt.Sprintf("%x", e.refDeltaBase)]
			}
			if !haveBase {
				continue
			}
			base, ok := memo[baseOffset]
			if !ok {
				continue
			}
			content, err := applyDelta(base.content, e.data)
			if err != nil {
				return fmt.Errorf("resolving delta at offset %d: %w", e.offset, err)
			}
			sha, err := writeObject(packObjTypeName(base.typ), content, true)
			if err != nil {
				return err
			}
			memo[e.offset] = resolvedObj{typ: base.typ, content: content}
			shaToOffset[sha] = e.offset
			progressed = true
		}
	}

	for _, e := range entries {
		if _, ok := memo[e.offset]; !ok {
			return fmt.Errorf("could not resolve object at offset %d (missing delta base)", e.offset)
		}
	}
	return nil
}
