package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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

func main() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("$ ")

		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSuffix(line, "\n")

		runLine(line)
	}
}
