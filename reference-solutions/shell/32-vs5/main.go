package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// enableCbreakMode disables canonical (line-buffered) input and local echo
// so the shell can read and react to individual keystrokes (e.g. Tab),
// while deliberately leaving OPOST/ONLCR untouched so "\n" still gets
// translated to "\r\n" for every writer (our own output and any child
// process's) — unlike a full raw mode, which would break that translation
// and garble output on the next line.
func enableCbreakMode(fd int) (*unix.Termios, error) {
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	orig := *termios
	termios.Lflag &^= unix.ICANON | unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return nil, err
	}
	return &orig, nil
}

func restoreTermios(fd int, state *unix.Termios) {
	unix.IoctlSetTermios(fd, unix.TCSETS, state)
}

var builtins = map[string]bool{
	"exit": true,
	"echo": true,
	"type": true,
	"pwd":  true,
	"cd":   true,
}

func isBuiltin(name string) bool {
	return builtins[name]
}

// parseArgs splits a command line into arguments, honoring single-quoted
// spans (literal, no escaping) and collapsing unquoted whitespace.
func parseArgs(line string) []string {
	var args []string
	var current strings.Builder
	inArg := false
	i := 0
	n := len(line)

	for i < n {
		c := line[i]
		switch {
		case c == '\'':
			inArg = true
			i++
			for i < n && line[i] != '\'' {
				current.WriteByte(line[i])
				i++
			}
			i++
		case c == '"':
			inArg = true
			i++
			for i < n && line[i] != '"' {
				if line[i] == '\\' && i+1 < n && strings.ContainsRune(`"\$`+"`", rune(line[i+1])) {
					current.WriteByte(line[i+1])
					i += 2
				} else {
					current.WriteByte(line[i])
					i++
				}
			}
			i++
		case c == '\\':
			inArg = true
			if i+1 < n {
				current.WriteByte(line[i+1])
				i += 2
			} else {
				i++
			}
		case c == ' ' || c == '\t':
			if inArg {
				args = append(args, current.String())
				current.Reset()
				inArg = false
			}
			i++
		default:
			inArg = true
			current.WriteByte(c)
			i++
		}
	}
	if inArg {
		args = append(args, current.String())
	}
	return args
}

// extractRedirection pulls ">"/"1>" stdout and "2>" stderr redirect targets
// (plus their ">>"/"1>>" append variants) out of tokens, returning the
// remaining command tokens and the targets (empty if none was present).
func extractRedirection(tokens []string) (cmd []string, stdoutFile string, stdoutAppend bool, stderrFile string, stderrAppend bool) {
	i := 0
	for i < len(tokens) {
		switch tokens[i] {
		case ">", "1>":
			stdoutFile = tokens[i+1]
			stdoutAppend = false
			i += 2
		case ">>", "1>>":
			stdoutFile = tokens[i+1]
			stdoutAppend = true
			i += 2
		case "2>":
			stderrFile = tokens[i+1]
			stderrAppend = false
			i += 2
		case "2>>":
			stderrFile = tokens[i+1]
			stderrAppend = true
			i += 2
		default:
			cmd = append(cmd, tokens[i])
			i++
		}
	}
	return
}

func runLine(line string) {
	fields, stdoutFile, stdoutAppend, stderrFile, stderrAppend := extractRedirection(parseArgs(line))
	if len(fields) == 0 {
		return
	}
	command := fields[0]
	args := fields[1:]

	if stdoutFile != "" {
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if stdoutAppend {
			flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(stdoutFile, flags, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", stdoutFile, err)
			return
		}
		defer f.Close()
		prevStdout := os.Stdout
		os.Stdout = f
		defer func() { os.Stdout = prevStdout }()
	}
	if stderrFile != "" {
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if stderrAppend {
			flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(stderrFile, flags, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", stderrFile, err)
			return
		}
		defer f.Close()
		prevStderr := os.Stderr
		os.Stderr = f
		defer func() { os.Stderr = prevStderr }()
	}

	switch command {
	case "exit":
		os.Exit(0)
	case "echo":
		fmt.Println(strings.Join(args, " "))
	case "cd":
		target := args[0]
		if target == "~" {
			target = os.Getenv("HOME")
		}
		if err := os.Chdir(target); err != nil {
			fmt.Printf("cd: %s: No such file or directory\n", target)
		}
	case "pwd":
		dir, _ := os.Getwd()
		fmt.Println(dir)
	case "type":
		target := args[0]
		if isBuiltin(target) {
			fmt.Printf("%s is a shell builtin\n", target)
		} else if path, err := exec.LookPath(target); err == nil {
			fmt.Printf("%s is %s\n", target, path)
		} else {
			fmt.Printf("%s: not found\n", target)
		}
	default:
		path, err := exec.LookPath(command)
		if err != nil {
			fmt.Printf("%s: command not found\n", command)
			return
		}
		cmd := exec.Command(path, args...)
		cmd.Args[0] = command
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Run()
	}
}

// findExecutableMatches returns the names of executables in PATH whose name
// starts with prefix, deduplicated.
func findExecutableMatches(prefix string) []string {
	if prefix == "" {
		return nil
	}
	seen := map[string]bool{}
	var matches []string
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, prefix) || seen[name] {
				continue
			}
			info, err := e.Info()
			if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
				continue
			}
			seen[name] = true
			matches = append(matches, name)
		}
	}
	return matches
}

// matchesFor returns every builtin/executable name matching prefix,
// deduplicated and sorted alphabetically.
func matchesFor(prefix string) []string {
	if prefix == "" {
		return nil
	}
	seen := map[string]bool{}
	var matches []string
	for _, name := range []string{"echo", "exit"} {
		if strings.HasPrefix(name, prefix) && !seen[name] {
			seen[name] = true
			matches = append(matches, name)
		}
	}
	for _, name := range findExecutableMatches(prefix) {
		if !seen[name] {
			seen[name] = true
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return matches
}

// longestCommonPrefix returns the longest prefix shared by every string in strs.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		for !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// findFileMatchesInCurrentDir returns paths matching word: entries in the
// current directory if word has no "/", or entries in the directory named
// by word's last "/"-separated component otherwise. Returned paths keep
// word's own directory prefix (e.g. "path/to/f" -> "path/to/file.txt").
func findFileMatchesInCurrentDir(word string) []string {
	dirPrefix := ""
	prefix := word
	searchDir := "."
	if idx := strings.LastIndex(word, "/"); idx >= 0 {
		dirPrefix = word[:idx+1]
		searchDir = dirPrefix
		prefix = word[idx+1:]
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var matches []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			matches = append(matches, dirPrefix+name)
		}
	}
	return matches
}

// readLine reads one line of input in raw terminal mode, echoing typed
// characters, handling backspace, and completing on Tab.
func readLine(reader *bufio.Reader) (string, bool) {
	var buf []byte
	lastTabPrefix := "" // prefix of the previous Tab press that had multiple matches, "" otherwise

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return string(buf), false
		}
		switch b {
		case '\r', '\n':
			fmt.Print("\r\n")
			return string(buf), true
		case 127, '\b':
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Print("\b \b")
			}
			lastTabPrefix = ""
		case '\t':
			lastSpace := strings.LastIndex(string(buf), " ")
			if lastSpace == -1 {
				matches := matchesFor(string(buf))
				switch len(matches) {
				case 0:
					fmt.Print("\a")
				case 1:
					completed := matches[0] + " "
					for range buf {
						fmt.Print("\b \b")
					}
					buf = []byte(completed)
					fmt.Print(completed)
				default:
					lcp := longestCommonPrefix(matches)
					if len(lcp) > len(buf) {
						for range buf {
							fmt.Print("\b \b")
						}
						buf = []byte(lcp)
						fmt.Print(lcp)
						lastTabPrefix = ""
					} else if lastTabPrefix == string(buf) {
						fmt.Print("\r\n" + strings.Join(matches, "  ") + "\r\n$ " + string(buf))
						lastTabPrefix = ""
					} else {
						fmt.Print("\a")
						lastTabPrefix = string(buf)
					}
				}
			} else {
				word := string(buf[lastSpace+1:])
				matches := findFileMatchesInCurrentDir(word)
				if len(matches) == 1 {
					completed := matches[0]
					if !strings.HasSuffix(completed, "/") {
						completed += " "
					}
					newBuf := string(buf[:lastSpace+1]) + completed
					for range buf {
						fmt.Print("\b \b")
					}
					buf = []byte(newBuf)
					fmt.Print(newBuf)
				} else if len(matches) == 0 {
					fmt.Print("\a")
				}
			}
		default:
			buf = append(buf, b)
			fmt.Printf("%c", b)
			lastTabPrefix = ""
		}
	}
}

func main() {
	fd := int(os.Stdin.Fd())
	if oldState, err := enableCbreakMode(fd); err == nil {
		defer restoreTermios(fd, oldState)
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("$ ")

		line, ok := readLine(reader)
		if !ok {
			break
		}

		runLine(line)
	}
}
