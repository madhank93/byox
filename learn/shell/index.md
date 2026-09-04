---
title: Build your own Shell — primer
concepts: [process control, tokenizing, file descriptors, terminal modes]
---

## The mental model

A shell is a REPL whose "evaluate" step is `fork`/`exec`. Read a line, split it
into words, decide whether the first word is a builtin or a program on `PATH`,
run it, wire up its file descriptors, wait, repeat. The course spends most of
its stages on the two places that are harder than they look: **splitting the
line correctly** and **not being the process that keeps stdin**.

## Builtin vs external, and why it matters

`exit`, `echo`, `type`, `pwd` and `cd` must be builtins because they change or
report the shell's own state — a child process cannot change its parent's
working directory. `type` is the stage that forces you to model the lookup
order explicitly: builtin first, then each `PATH` entry in order, first
executable match wins.

In Go, external commands are `os/exec`: `exec.Command`, then `cmd.Stdin/Stdout/
Stderr`, then `Run`. Note the detail the tester checks — `argv[0]` is the name
the program was invoked as, which is not always the path you resolved.

## Quoting is a state machine

This is where naive `strings.Fields` dies:

- Single quotes: everything literal, no escapes at all.
- Double quotes: literal *except* `\` before `$`, `` ` ``, `"`, `\`, newline.
- Backslash outside quotes: escapes the next character.
- Quotes are removed after splitting, and `a"b"c` is one word `abc`.

Write it as an explicit scanner over runes with a small state (`plain`,
`inSingle`, `inDouble`) building the current word. Every later stage — pipelines,
redirection, completion — depends on this being right.

## Redirection and pipelines

`>` `1>` `2>` `>>` `2>>` are not arguments; strip them during parsing and open
the target with `os.OpenFile` (`O_CREATE|O_WRONLY|O_TRUNC` or `O_APPEND`).
Redirection applies to *one* command, so it is part of the command's structure,
not the line's.

A pipeline connects each command's stdout to the next one's stdin — `io.Pipe` or
`cmd.StdoutPipe`, all processes started **before** any is waited on, then all
waited. Starting and waiting one at a time deadlocks as soon as a program fills
a pipe buffer.

## The terminal

Tab completion means you can no longer read whole lines: you need raw mode, so
keystrokes arrive un-buffered and un-echoed, and you draw the line yourself —
including handling backspace, bell on ambiguity, and printing the common prefix
of multiple matches. `golang.org/x/term` gives you `MakeRaw`; whatever you do,
restore the old state on exit or you leave the user's terminal broken.

History adds persistence on top: an in-memory ring, `history -r/-w/-a`, and the
`HISTFILE` conventions on startup and exit.

## The stage arc

REPL → invalid commands → builtins (`exit`, `echo`, `type`) → `PATH` lookup →
`cd` → quoting → redirection → completion (builtin, then executables, then
programmable) → pipelines → history → history files → shell variables.

## Going deeper

- [POSIX shell command language](https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html)
- [`os/exec` package docs](https://pkg.go.dev/os/exec)
- [`golang.org/x/term` — raw mode](https://pkg.go.dev/golang.org/x/term)
