package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ./your_program.sh tokenize|parse <filename>")
		os.Exit(1)
	}

	command := os.Args[1]

	filename := os.Args[2]
	fileContents, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	tokens, errs := scanTokens(string(fileContents))

	switch command {
	case "tokenize":
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		for _, token := range tokens {
			fmt.Println(token)
		}
		if len(errs) > 0 {
			os.Exit(65)
		}
	case "parse":
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		if len(errs) > 0 {
			os.Exit(65)
		}
		expr := NewParser(tokens).parseExpression()
		fmt.Println(expr.String())
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}

// Token is one lexeme recognized by the scanner, printed as
// "<type> <lexeme> <literal>".
type Token struct {
	Type    string
	Lexeme  string
	Literal string
}

var keywords = map[string]string{
	"and":    "AND",
	"class":  "CLASS",
	"else":   "ELSE",
	"false":  "FALSE",
	"for":    "FOR",
	"fun":    "FUN",
	"if":     "IF",
	"nil":    "NIL",
	"or":     "OR",
	"print":  "PRINT",
	"return": "RETURN",
	"super":  "SUPER",
	"this":   "THIS",
	"true":   "TRUE",
	"var":    "VAR",
	"while":  "WHILE",
}

func (t Token) String() string {
	return fmt.Sprintf("%s %s %s", t.Type, t.Lexeme, t.Literal)
}

// scanTokens turns source into a list of tokens (always ending with EOF)
// and a list of "[line N] Error: ..." messages for any invalid characters.
func scanTokens(source string) ([]Token, []string) {
	var tokens []Token
	var errors []string
	line := 1
	i := 0

	// matchNext consumes source[i] if it equals want, advancing i; used for
	// two-character operators like "==" that share a prefix with a
	// one-character operator like "=".
	matchNext := func(want byte) bool {
		if i < len(source) && source[i] == want {
			i++
			return true
		}
		return false
	}

	for i < len(source) {
		c := source[i]
		i++
		switch c {
		case '(':
			tokens = append(tokens, Token{"LEFT_PAREN", "(", "null"})
		case ')':
			tokens = append(tokens, Token{"RIGHT_PAREN", ")", "null"})
		case '{':
			tokens = append(tokens, Token{"LEFT_BRACE", "{", "null"})
		case '}':
			tokens = append(tokens, Token{"RIGHT_BRACE", "}", "null"})
		case ',':
			tokens = append(tokens, Token{"COMMA", ",", "null"})
		case '.':
			tokens = append(tokens, Token{"DOT", ".", "null"})
		case '-':
			tokens = append(tokens, Token{"MINUS", "-", "null"})
		case '+':
			tokens = append(tokens, Token{"PLUS", "+", "null"})
		case ';':
			tokens = append(tokens, Token{"SEMICOLON", ";", "null"})
		case '*':
			tokens = append(tokens, Token{"STAR", "*", "null"})
		case '/':
			if matchNext('/') {
				for i < len(source) && source[i] != '\n' {
					i++
				}
			} else {
				tokens = append(tokens, Token{"SLASH", "/", "null"})
			}
		case '=':
			if matchNext('=') {
				tokens = append(tokens, Token{"EQUAL_EQUAL", "==", "null"})
			} else {
				tokens = append(tokens, Token{"EQUAL", "=", "null"})
			}
		case '!':
			if matchNext('=') {
				tokens = append(tokens, Token{"BANG_EQUAL", "!=", "null"})
			} else {
				tokens = append(tokens, Token{"BANG", "!", "null"})
			}
		case '<':
			if matchNext('=') {
				tokens = append(tokens, Token{"LESS_EQUAL", "<=", "null"})
			} else {
				tokens = append(tokens, Token{"LESS", "<", "null"})
			}
		case '>':
			if matchNext('=') {
				tokens = append(tokens, Token{"GREATER_EQUAL", ">=", "null"})
			} else {
				tokens = append(tokens, Token{"GREATER", ">", "null"})
			}
		case ' ', '\t', '\r':
			// ignored
		case '\n':
			line++
		case '"':
			start := i
			startLine := line
			for i < len(source) && source[i] != '"' {
				if source[i] == '\n' {
					line++
				}
				i++
			}
			if i >= len(source) {
				errors = append(errors, fmt.Sprintf("[line %d] Error: Unterminated string.", startLine))
			} else {
				value := source[start:i]
				i++ // consume closing quote
				tokens = append(tokens, Token{"STRING", `"` + value + `"`, value})
			}
		default:
			if isDigit(c) {
				start := i - 1
				for i < len(source) && isDigit(source[i]) {
					i++
				}
				if i < len(source) && source[i] == '.' && i+1 < len(source) && isDigit(source[i+1]) {
					i++
					for i < len(source) && isDigit(source[i]) {
						i++
					}
				}
				lexeme := source[start:i]
				value, _ := strconv.ParseFloat(lexeme, 64)
				literal := strconv.FormatFloat(value, 'f', -1, 64)
				if !strings.Contains(literal, ".") {
					literal += ".0"
				}
				tokens = append(tokens, Token{"NUMBER", lexeme, literal})
			} else if isAlpha(c) {
				start := i - 1
				for i < len(source) && isAlphaNumeric(source[i]) {
					i++
				}
				text := source[start:i]
				tokenType := "IDENTIFIER"
				if kw, ok := keywords[text]; ok {
					tokenType = kw
				}
				tokens = append(tokens, Token{tokenType, text, "null"})
			} else {
				errors = append(errors, fmt.Sprintf("[line %d] Error: Unexpected character: %c", line, c))
			}
		}
	}
	tokens = append(tokens, Token{"EOF", "", "null"})
	return tokens, errors
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isAlphaNumeric(c byte) bool {
	return isAlpha(c) || isDigit(c)
}

// Expr is a parsed Lox expression, printable in the book's Lisp-like AST
// format (e.g. "(+ 2.0 3.0)").
type Expr interface {
	String() string
}

// LiteralExpr is a literal value (true/false/nil/number/string), holding
// its already-formatted AST-printer text directly.
type LiteralExpr struct {
	Text string
}

func (e LiteralExpr) String() string {
	return e.Text
}

// Parser turns a token list into an Expr tree via recursive descent.
type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens}
}

func (p *Parser) parseExpression() Expr {
	return p.primary()
}

func (p *Parser) primary() Expr {
	tok := p.tokens[p.pos]
	switch tok.Type {
	case "TRUE":
		p.pos++
		return LiteralExpr{"true"}
	case "FALSE":
		p.pos++
		return LiteralExpr{"false"}
	case "NIL":
		p.pos++
		return LiteralExpr{"nil"}
	case "NUMBER":
		p.pos++
		return LiteralExpr{tok.Literal}
	}
	return nil
}
