package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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

// completeBuiltin returns a completed builtin name (with a trailing space)
// if prefix uniquely identifies the start of one.
func completeBuiltin(prefix string) (string, bool) {
	if prefix == "" {
		return "", false
	}
	for _, name := range []string{"echo", "exit"} {
		if strings.HasPrefix(name, prefix) {
			return name + " ", true
		}
	}
	return "", false
}

// readLine reads one line of input in raw terminal mode, echoing typed
// characters, handling backspace, and completing on Tab.
func readLine(reader *bufio.Reader) (string, bool) {
	var buf []byte
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
		case '\t':
			if completed, ok := completeBuiltin(string(buf)); ok {
				for range buf {
					fmt.Print("\b \b")
				}
				buf = []byte(completed)
				fmt.Print(completed)
			}
		default:
			buf = append(buf, b)
			fmt.Printf("%c", b)
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
