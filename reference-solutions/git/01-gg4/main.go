package main

import (
	"fmt"
	"os"
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
