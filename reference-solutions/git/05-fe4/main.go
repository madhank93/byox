package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
