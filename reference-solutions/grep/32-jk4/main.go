package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func collectFilesRecursive(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// Usage: echo <input_text> | your_program.sh -E <pattern>
//    or: your_program.sh [-r] -E <pattern> <file...>
func main() {
	args := os.Args[1:]
	recursive := false
	if len(args) > 0 && args[0] == "-r" {
		recursive = true
		args = args[1:]
	}
	onlyMatching := false
	if len(args) > 0 && args[0] == "-o" {
		onlyMatching = true
		args = args[1:]
	}
	colorAlways := false
	if len(args) > 0 && strings.HasPrefix(args[0], "--color=") {
		colorAlways = args[0] == "--color=always"
		args = args[1:]
	}
	if len(args) < 2 || args[0] != "-E" {
		fmt.Fprintf(os.Stderr, "usage: mygrep [-r] [-o] [--color=always] -E <pattern> [file...]\n")
		os.Exit(2)
	}

	pattern := args[1]

	var files []string
	if recursive {
		var err error
		files, err = collectFilesRecursive(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}
	} else {
		files = args[2:]
	}
	searchingFiles := len(files) > 0
	prefixWithFilename := recursive || len(files) > 1

	matchedAny := false

	searchContent := func(content []byte, prefix string) {
		lines := bytes.Split(bytes.TrimSuffix(content, []byte("\n")), []byte("\n"))
		for _, line := range lines {
			if onlyMatching {
				for _, m := range findAllMatches(line, pattern) {
					matchedAny = true
					fmt.Println(string(line[m[0]:m[1]]))
				}
				continue
			}

			outputLine := line
			if colorAlways {
				matches := findAllMatches(line, pattern)
				if len(matches) == 0 {
					continue
				}
				matchedAny = true
				outputLine = highlightMatches(line, matches)
			} else {
				ok, err := matchLine(line, pattern)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(2)
				}
				if !ok {
					continue
				}
				matchedAny = true
			}
			if prefixWithFilename {
				fmt.Printf("%s:%s\n", prefix, outputLine)
			} else {
				fmt.Println(string(outputLine))
			}
		}
	}

	if searchingFiles {
		for _, file := range files {
			content, err := os.ReadFile(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: read %s: %v\n", file, err)
				os.Exit(2)
			}
			searchContent(content, file)
		}
	} else {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read input text: %v\n", err)
			os.Exit(2)
		}
		searchContent(content, "")
	}

	if !matchedAny {
		os.Exit(1)
	}
}

// charAtom matches a single byte of input.
type charAtom interface {
	matches(b byte) bool
}

type literalAtom struct{ ch byte }

func (a literalAtom) matches(b byte) bool { return b == a.ch }

type digitAtom struct{}

func (digitAtom) matches(b byte) bool { return isDigit(b) }

type wordAtom struct{}

func (wordAtom) matches(b byte) bool { return isWordChar(b) }

type anyAtom struct{}

func (anyAtom) matches(b byte) bool { return true }

type groupAtom struct {
	chars  string
	negate bool
}

func (g groupAtom) matches(b byte) bool {
	contains := bytes.ContainsRune([]byte(g.chars), rune(b))
	if g.negate {
		return !contains
	}
	return contains
}

// node is a pattern element that attempts to match starting at pos, calling
// cont with the position after the match. It backtracks (tries a different
// way of matching itself) if cont returns false, and returns false itself
// if no way of matching leads to an overall success.
type node interface {
	match(ctx *matchCtx, pos int, cont func(int) bool) bool
}

type capture struct{ start, end int }

type matchCtx struct {
	line   []byte
	groups []capture // index 0 unused; indices 1..N are capture groups
}

// charNode matches a single charAtom against one byte of input.
type charNode struct{ a charAtom }

func (n charNode) match(ctx *matchCtx, pos int, cont func(int) bool) bool {
	if pos < len(ctx.line) && n.a.matches(ctx.line[pos]) {
		return cont(pos + 1)
	}
	return false
}

// quantifierNode repeats inner between min and max times (max == -1 means
// unbounded), matching greedily and backtracking to fewer repetitions if
// the continuation fails.
type quantifierNode struct {
	inner    node
	min, max int
}

func (q quantifierNode) match(ctx *matchCtx, pos int, cont func(int) bool) bool {
	return matchRepeat(q.inner, ctx, pos, 0, q.min, q.max, cont)
}

func matchRepeat(inner node, ctx *matchCtx, pos, count, min, max int, cont func(int) bool) bool {
	if max < 0 || count < max {
		if inner.match(ctx, pos, func(p int) bool {
			return matchRepeat(inner, ctx, p, count+1, min, max, cont)
		}) {
			return true
		}
	}
	if count >= min {
		return cont(pos)
	}
	return false
}

func matchSeq(nodes []node, ctx *matchCtx, pos int, cont func(int) bool) bool {
	if len(nodes) == 0 {
		return cont(pos)
	}
	return nodes[0].match(ctx, pos, func(p int) bool {
		return matchSeq(nodes[1:], ctx, p, cont)
	})
}

// groupNode matches any one of its alternatives, e.g. (cat|dog). The matched
// span is recorded as capture group `index` for \N backreferences to reuse.
type groupNode struct {
	index        int
	alternatives [][]node
}

func (g groupNode) match(ctx *matchCtx, pos int, cont func(int) bool) bool {
	for _, alt := range g.alternatives {
		ok := matchSeq(alt, ctx, pos, func(end int) bool {
			prev := ctx.groups[g.index]
			ctx.groups[g.index] = capture{pos, end}
			if cont(end) {
				return true
			}
			ctx.groups[g.index] = prev
			return false
		})
		if ok {
			return true
		}
	}
	return false
}

// backreferenceNode matches whatever text capture group `index` last captured.
type backreferenceNode struct{ index int }

func (b backreferenceNode) match(ctx *matchCtx, pos int, cont func(int) bool) bool {
	g := ctx.groups[b.index]
	if g.start < 0 {
		return false
	}
	captured := ctx.line[g.start:g.end]
	if pos+len(captured) > len(ctx.line) || !bytes.Equal(ctx.line[pos:pos+len(captured)], captured) {
		return false
	}
	return cont(pos + len(captured))
}

// findMatchingParen returns the index of the ')' matching the '(' at open.
func findMatchingParen(pattern string, open int) int {
	depth := 1
	for i := open + 1; i < len(pattern); i++ {
		switch pattern[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits s on sep, ignoring occurrences of sep nested inside parens.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isWordChar(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func parsePattern(pattern string, groupCount *int) []node {
	var nodes []node
	i := 0
	for i < len(pattern) {
		var n node
		switch {
		case pattern[i] == '\\' && i+1 < len(pattern) && pattern[i+1] == 'd':
			n = charNode{digitAtom{}}
			i += 2
		case pattern[i] == '\\' && i+1 < len(pattern) && pattern[i+1] == 'w':
			n = charNode{wordAtom{}}
			i += 2
		case pattern[i] == '\\' && i+1 < len(pattern) && pattern[i+1] >= '1' && pattern[i+1] <= '9':
			n = backreferenceNode{index: int(pattern[i+1] - '0')}
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
			n = charNode{groupAtom{chars: inner, negate: negate}}
			i = end + 1
		case pattern[i] == '.':
			n = charNode{anyAtom{}}
			i++
		case pattern[i] == '(':
			end := findMatchingParen(pattern, i)
			inner := pattern[i+1 : end]
			*groupCount++
			index := *groupCount
			var alternatives [][]node
			for _, alt := range splitTopLevel(inner, '|') {
				alternatives = append(alternatives, parsePattern(alt, groupCount))
			}
			n = groupNode{index: index, alternatives: alternatives}
			i = end + 1
		default:
			n = charNode{literalAtom{ch: pattern[i]}}
			i++
		}

		switch {
		case i < len(pattern) && pattern[i] == '+':
			n = quantifierNode{inner: n, min: 1, max: -1}
			i++
		case i < len(pattern) && pattern[i] == '?':
			n = quantifierNode{inner: n, min: 0, max: 1}
			i++
		case i < len(pattern) && pattern[i] == '*':
			n = quantifierNode{inner: n, min: 0, max: -1}
			i++
		case i < len(pattern) && pattern[i] == '{':
			end := strings.IndexByte(pattern[i:], '}') + i
			inner := pattern[i+1 : end]
			if commaIdx := strings.IndexByte(inner, ','); commaIdx >= 0 {
				min, _ := strconv.Atoi(inner[:commaIdx])
				max := -1
				if commaIdx+1 < len(inner) {
					max, _ = strconv.Atoi(inner[commaIdx+1:])
				}
				n = quantifierNode{inner: n, min: min, max: max}
			} else {
				count, _ := strconv.Atoi(inner)
				n = quantifierNode{inner: n, min: count, max: count}
			}
			i = end + 1
		}

		nodes = append(nodes, n)
	}
	return nodes
}

// findMatch finds the first (leftmost) match of pattern in line, returning
// the matched span [start, end).
func findMatch(line []byte, pattern string, searchFrom int) (start, end int, ok bool) {
	anchoredStart := strings.HasPrefix(pattern, "^")
	if anchoredStart {
		pattern = pattern[1:]
	}
	anchoredEnd := strings.HasSuffix(pattern, "$")
	if anchoredEnd {
		pattern = pattern[:len(pattern)-1]
	}
	groupCount := 0
	nodes := parsePattern(pattern, &groupCount)

	tryAt := func(s int) (int, bool) {
		groups := make([]capture, groupCount+1)
		for i := range groups {
			groups[i] = capture{-1, -1}
		}
		ctx := &matchCtx{line: line, groups: groups}
		var e int
		matched := matchSeq(nodes, ctx, s, func(p int) bool {
			if anchoredEnd && p != len(line) {
				return false
			}
			e = p
			return true
		})
		return e, matched
	}

	if anchoredStart {
		if searchFrom > 0 {
			return 0, 0, false
		}
		e, matched := tryAt(0)
		return 0, e, matched
	}
	for s := searchFrom; s <= len(line); s++ {
		if e, matched := tryAt(s); matched {
			return s, e, true
		}
	}
	return 0, 0, false
}

func matchLine(line []byte, pattern string) (bool, error) {
	_, _, ok := findMatch(line, pattern, 0)
	return ok, nil
}

// highlightMatches wraps each matched span in line with ANSI bold-red escape sequences.
func highlightMatches(line []byte, matches [][2]int) []byte {
	var buf bytes.Buffer
	pos := 0
	for _, m := range matches {
		buf.Write(line[pos:m[0]])
		buf.WriteString("\033[01;31m")
		buf.Write(line[m[0]:m[1]])
		buf.WriteString("\033[m")
		pos = m[1]
	}
	buf.Write(line[pos:])
	return buf.Bytes()
}

// findAllMatches finds every non-overlapping match of pattern in line, in order.
func findAllMatches(line []byte, pattern string) [][2]int {
	var matches [][2]int
	pos := 0
	for pos <= len(line) {
		start, end, ok := findMatch(line, pattern, pos)
		if !ok {
			break
		}
		matches = append(matches, [2]int{start, end})
		if end == start {
			pos = start + 1
		} else {
			pos = end
		}
	}
	return matches
}
