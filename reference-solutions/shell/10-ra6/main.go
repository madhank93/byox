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

func main() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("$ ")

		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSuffix(line, "\n")

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		command := fields[0]
		args := fields[1:]

		switch command {
		case "exit":
			os.Exit(0)
		case "echo":
			fmt.Println(strings.Join(args, " "))
		case "cd":
			target := args[0]
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
				continue
			}
			cmd := exec.Command(path, args...)
			cmd.Args[0] = command
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			cmd.Run()
		}
	}
}
