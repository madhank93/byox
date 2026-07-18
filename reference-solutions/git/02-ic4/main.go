package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
