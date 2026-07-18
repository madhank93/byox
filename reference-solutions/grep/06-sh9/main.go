package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// Usage: echo <input_text> | your_program.sh -E <pattern>
func main() {
	if len(os.Args) < 3 || os.Args[1] != "-E" {
		fmt.Fprintf(os.Stderr, "usage: mygrep -E <pattern>\n")
		os.Exit(2)
	}

	pattern := os.Args[2]

	line, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read input text: %v\n", err)
		os.Exit(2)
	}

	ok, err := matchLine(line, pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	if !ok {
		os.Exit(1)
	}
}

type atom interface {
	match(b byte) bool
}

type literalAtom struct{ ch byte }

func (a literalAtom) match(b byte) bool { return b == a.ch }

type digitAtom struct{}

func (digitAtom) match(b byte) bool { return isDigit(b) }

type wordAtom struct{}

func (wordAtom) match(b byte) bool { return isWordChar(b) }

type groupAtom struct {
	chars  string
	negate bool
}

func (g groupAtom) match(b byte) bool {
	contains := bytes.ContainsRune([]byte(g.chars), rune(b))
	if g.negate {
		return !contains
	}
	return contains
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isWordChar(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func parsePattern(pattern string) []atom {
	var atoms []atom
	i := 0
	for i < len(pattern) {
		switch {
		case pattern[i] == '\\' && i+1 < len(pattern) && pattern[i+1] == 'd':
			atoms = append(atoms, digitAtom{})
			i += 2
		case pattern[i] == '\\' && i+1 < len(pattern) && pattern[i+1] == 'w':
			atoms = append(atoms, wordAtom{})
			i += 2
		case pattern[i] == '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			inner := pattern[i+1 : end]
			negate := strings.HasPrefix(inner, "^")
			if negate {
				inner = inner[1:]
			}
			atoms = append(atoms, groupAtom{chars: inner, negate: negate})
			i = end + 1
		default:
			atoms = append(atoms, literalAtom{ch: pattern[i]})
			i++
		}
	}
	return atoms
}

func matchAtomsAt(atoms []atom, line []byte, start int) bool {
	pos := start
	for _, a := range atoms {
		if pos >= len(line) || !a.match(line[pos]) {
			return false
		}
		pos++
	}
	return true
}

func matchLine(line []byte, pattern string) (bool, error) {
	atoms := parsePattern(pattern)
	for start := 0; start <= len(line); start++ {
		if matchAtomsAt(atoms, line, start) {
			return true, nil
		}
	}
	return false, nil
}
