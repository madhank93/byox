package main

import (
	"fmt"
	"os"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ./your_program.sh tokenize <filename>")
		os.Exit(1)
	}

	command := os.Args[1]

	if command != "tokenize" {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}

	filename := os.Args[2]
	fileContents, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	for _, token := range scanTokens(string(fileContents)) {
		fmt.Println(token)
	}
}

// Token is one lexeme recognized by the scanner, printed as
// "<type> <lexeme> <literal>".
type Token struct {
	Type    string
	Lexeme  string
	Literal string
}

func (t Token) String() string {
	return fmt.Sprintf("%s %s %s", t.Type, t.Lexeme, t.Literal)
}

// scanTokens turns source into a list of tokens, always ending with EOF.
func scanTokens(source string) []Token {
	var tokens []Token
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '(':
			tokens = append(tokens, Token{"LEFT_PAREN", "(", "null"})
		case ')':
			tokens = append(tokens, Token{"RIGHT_PAREN", ")", "null"})
		case '{':
			tokens = append(tokens, Token{"LEFT_BRACE", "{", "null"})
		case '}':
			tokens = append(tokens, Token{"RIGHT_BRACE", "}", "null"})
		}
	}
	tokens = append(tokens, Token{"EOF", "", "null"})
	return tokens
}
