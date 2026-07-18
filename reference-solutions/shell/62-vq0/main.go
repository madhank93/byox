package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
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
	"exit":     true,
	"echo":     true,
	"type":     true,
	"pwd":      true,
	"cd":       true,
	"complete": true,
	"jobs":     true,
	"history":  true,
}

// Job tracks one background command started with a trailing "&". Done is
// closed by a dedicated goroutine once Cmd.Wait() returns, so the jobs
// builtin can check for completion (and reap the zombie) without blocking.
type Job struct {
	Number  int
	Cmd     *exec.Cmd
	Command string
	Done    chan struct{}
}

var jobs []*Job

// history holds every non-blank line entered, in entry order, 1-indexed
// when displayed by the history builtin.
var history []string

// nextJobNumber returns the next job number to assign: 1 if the table is
// empty, otherwise one more than the highest number currently in use.
// Numbers are recycled as jobs are reaped, so this can't be a monotonic
// counter.
func nextJobNumber() int {
	max := 0
	for _, j := range jobs {
		if j.Number > max {
			max = j.Number
		}
	}
	return max + 1
}

// reapJobs checks every background job for completion, printing and
// removing each one that has finished. Running jobs are only printed when
// showRunning is true (the jobs builtin lists everything; the automatic
// reap before each prompt only announces newly-finished jobs).
func reapJobs(out io.Writer, showRunning bool) {
	var remaining []*Job
	for i, j := range jobs {
		status := "Running"
		select {
		case <-j.Done:
			status = "Done"
		default:
		}
		marker := " "
		if i == len(jobs)-1 {
			marker = "+"
		} else if i == len(jobs)-2 {
			marker = "-"
		}
		if status == "Running" {
			if showRunning {
				fmt.Fprintf(out, "[%d]%s  %-24s%s &\n", j.Number, marker, status, j.Command)
			}
			remaining = append(remaining, j)
		} else {
			fmt.Fprintf(out, "[%d]%s  %-24s%s\n", j.Number, marker, status, j.Command)
		}
	}
	jobs = remaining
}

func isBuiltin(name string) bool {
	return builtins[name]
}

// completers maps a command name to its registered `complete -C` script path.
var completers = map[string]string{}

// runCompleter runs the completer script at path and returns its stdout
// lines as candidates (nil if it produced no output).
func runCompleter(path, command, word, prevWord, line string) []string {
	cmd := exec.Command(path, command, word, prevWord)
	cmd.Env = append(os.Environ(),
		"COMP_LINE="+line,
		fmt.Sprintf("COMP_POINT=%d", len(line)),
	)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
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

// splitPipeline splits tokens into the command segments of a pipeline,
// breaking on unquoted "|" tokens.
func splitPipeline(tokens []string) [][]string {
	var segments [][]string
	var current []string
	for _, t := range tokens {
		if t == "|" {
			segments = append(segments, current)
			current = nil
		} else {
			current = append(current, t)
		}
	}
	return append(segments, current)
}

// runPipeline connects each segment's stdout to the next segment's stdin
// via an OS pipe, starting every external command before waiting on any of
// them so they run concurrently. A builtin segment runs synchronously,
// in-process, writing to its end of the pipe — safe because none of our
// builtins read stdin, so there's nothing to run concurrently with.
func runPipeline(segments [][]string) {
	n := len(segments)
	cmds := make([]*exec.Cmd, 0, n)
	var stdin *os.File // read end of the previous segment's pipe, or nil for the first segment

	for i, seg := range segments {
		fields, _, _, _, _ := extractRedirection(seg)
		if len(fields) == 0 {
			return
		}
		command, args := fields[0], fields[1:]

		var pipeReader, pipeWriter *os.File
		if i < n-1 {
			var err error
			pipeReader, pipeWriter, err = os.Pipe()
			if err != nil {
				fmt.Printf("pipe: %v\n", err)
				return
			}
		}
		out := io.Writer(os.Stdout)
		if pipeWriter != nil {
			out = pipeWriter
		}

		if isBuiltin(command) {
			runBuiltin(command, args, out, os.Stderr)
			if pipeWriter != nil {
				pipeWriter.Close()
			}
		} else {
			path, err := exec.LookPath(command)
			if err != nil {
				fmt.Printf("%s: command not found\n", command)
				return
			}
			cmd := exec.Command(path, args...)
			cmd.Args[0] = command
			cmd.Stderr = os.Stderr
			cmd.Stdout = out
			if stdin != nil {
				cmd.Stdin = stdin
			} else {
				cmd.Stdin = os.Stdin
			}

			if err := cmd.Start(); err != nil {
				fmt.Printf("%v\n", err)
				return
			}
			// The child now holds its own dup of these fds; the parent's
			// copy must be closed so EOF propagates once the writer exits.
			if pipeWriter != nil {
				pipeWriter.Close()
			}
			cmds = append(cmds, cmd)
		}

		if stdin != nil {
			stdin.Close()
		}
		stdin = pipeReader
	}

	for _, cmd := range cmds {
		cmd.Wait()
	}
}

func runLine(line string) {
	tokens := parseArgs(line)
	background := false
	if len(tokens) > 0 && tokens[len(tokens)-1] == "&" {
		background = true
		tokens = tokens[:len(tokens)-1]
	}

	segments := splitPipeline(tokens)
	if len(segments) > 1 {
		runPipeline(segments)
		return
	}

	fields, stdoutFile, stdoutAppend, stderrFile, stderrAppend := extractRedirection(tokens)
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

	if isBuiltin(command) {
		runBuiltin(command, args, os.Stdout, os.Stderr)
		return
	}
	runExternal(command, args, fields, background)
}

// runBuiltin executes a builtin, writing to out/errOut — either the
// process's real stdout/stderr (possibly already redirected to a file by
// the caller) or one end of a pipeline pipe.
func runBuiltin(command string, args []string, out, errOut io.Writer) {
	switch command {
	case "exit":
		os.Exit(0)
	case "echo":
		fmt.Fprintln(out, strings.Join(args, " "))
	case "cd":
		target := args[0]
		if target == "~" {
			target = os.Getenv("HOME")
		}
		if err := os.Chdir(target); err != nil {
			fmt.Fprintf(out, "cd: %s: No such file or directory\n", target)
		}
	case "pwd":
		dir, _ := os.Getwd()
		fmt.Fprintln(out, dir)
	case "type":
		target := args[0]
		if isBuiltin(target) {
			fmt.Fprintf(out, "%s is a shell builtin\n", target)
		} else if path, err := exec.LookPath(target); err == nil {
			fmt.Fprintf(out, "%s is %s\n", target, path)
		} else {
			fmt.Fprintf(out, "%s: not found\n", target)
		}
	case "complete":
		if len(args) >= 2 && args[0] == "-p" {
			cmdName := args[1]
			if path, ok := completers[cmdName]; ok {
				fmt.Fprintf(out, "complete -C '%s' %s\n", path, cmdName)
			} else {
				fmt.Fprintf(out, "complete: %s: no completion specification\n", cmdName)
			}
		} else if len(args) >= 3 && args[0] == "-C" {
			completers[args[2]] = args[1]
		} else if len(args) >= 2 && args[0] == "-r" {
			delete(completers, args[1])
		}
	case "jobs":
		reapJobs(out, true)
	case "history":
		start := 0
		if len(args) >= 1 {
			if n, err := strconv.Atoi(args[0]); err == nil && n < len(history) {
				start = len(history) - n
			}
		}
		for i := start; i < len(history); i++ {
			fmt.Fprintf(out, "%5d  %s\n", i+1, history[i])
		}
	}
}

// runExternal runs a non-builtin command, backgrounding it (and tracking
// it as a job) when background is true. fields is command+args together,
// used verbatim as the job's display string.
func runExternal(command string, args []string, fields []string, background bool) {
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
	if background {
		if err := cmd.Start(); err != nil {
			fmt.Printf("%s: %v\n", command, err)
			return
		}
		job := &Job{Number: nextJobNumber(), Cmd: cmd, Command: strings.Join(fields, " "), Done: make(chan struct{})}
		jobs = append(jobs, job)
		fmt.Printf("[%d] %d\n", job.Number, cmd.Process.Pid)
		go func(j *Job) {
			j.Cmd.Wait()
			close(j.Done)
		}(job)
	} else {
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
	// tabArmed/tabArmedFor track whether the previous keystroke was an
	// ambiguous Tab press (multiple matches, bell rung) for this exact
	// prefix, so a second Tab on the same prefix lists matches instead of
	// ringing the bell again. A plain string sentinel can't tell "no
	// previous ambiguous Tab" apart from "previous Tab was for an empty
	// prefix" (both look like ""), so a separate bool is needed.
	tabArmed := false
	tabArmedFor := ""

	disarmTab := func() {
		tabArmed = false
	}

	// historyPos walks backward through history on repeated up-arrow
	// presses within this one line-editing session; it starts one past the
	// last entry (no recall active yet) and resets on every new readLine
	// call, i.e. every new prompt.
	historyPos := len(history)

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
			disarmTab()
		case 27: // ESC: start of an arrow-key escape sequence (ESC [ A/B/C/D)
			b1, err := reader.ReadByte()
			if err != nil || b1 != '[' {
				break
			}
			b2, err := reader.ReadByte()
			if err != nil {
				break
			}
			switch {
			case b2 == 'A' && historyPos > 0: // up arrow
				historyPos--
				for range buf {
					fmt.Print("\b \b")
				}
				buf = []byte(history[historyPos])
				fmt.Print(string(buf))
			case b2 == 'B' && historyPos < len(history): // down arrow
				historyPos++
				for range buf {
					fmt.Print("\b \b")
				}
				if historyPos == len(history) {
					buf = nil
				} else {
					buf = []byte(history[historyPos])
				}
				fmt.Print(string(buf))
			}
			disarmTab()
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
						disarmTab()
					} else if tabArmed && tabArmedFor == string(buf) {
						fmt.Print("\r\n" + strings.Join(matches, "  ") + "\r\n$ " + string(buf))
						disarmTab()
					} else {
						fmt.Print("\a")
						tabArmed = true
						tabArmedFor = string(buf)
					}
				}
			} else {
				word := string(buf[lastSpace+1:])
				commandName := string(buf[:strings.IndexByte(string(buf), ' ')])
				priorWords := strings.Fields(string(buf[:lastSpace]))
				prevWord := ""
				if len(priorWords) > 0 {
					prevWord = priorWords[len(priorWords)-1]
				}

				var matches []string
				if path, ok := completers[commandName]; ok {
					matches = runCompleter(path, commandName, word, prevWord, string(buf))
				} else {
					matches = findFileMatchesInCurrentDir(word)
				}
				switch len(matches) {
				case 0:
					fmt.Print("\a")
				case 1:
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
				default:
					lcp := longestCommonPrefix(matches)
					if len(lcp) > len(word) {
						newBuf := string(buf[:lastSpace+1]) + lcp
						for range buf {
							fmt.Print("\b \b")
						}
						buf = []byte(newBuf)
						fmt.Print(newBuf)
						disarmTab()
					} else if tabArmed && tabArmedFor == word {
						fmt.Print("\r\n" + strings.Join(matches, "  ") + "\r\n$ " + string(buf))
						disarmTab()
					} else {
						fmt.Print("\a")
						tabArmed = true
						tabArmedFor = word
					}
				}
			}
		default:
			buf = append(buf, b)
			fmt.Printf("%c", b)
			disarmTab()
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
		reapJobs(os.Stdout, false)
		fmt.Print("$ ")

		line, ok := readLine(reader)
		if !ok {
			break
		}
		if strings.TrimSpace(line) != "" {
			history = append(history, line)
		}

		runLine(line)
	}
}
