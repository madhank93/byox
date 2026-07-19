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
		expr, perr := NewParser(tokens).parseExpression()
		if perr != nil {
			fmt.Fprintln(os.Stderr, perr)
			os.Exit(65)
		}
		fmt.Println(expr.String())
	case "evaluate":
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		if len(errs) > 0 {
			os.Exit(65)
		}
		expr, perr := NewParser(tokens).parseExpression()
		if perr != nil {
			fmt.Fprintln(os.Stderr, perr)
			os.Exit(65)
		}
		value, everr := evaluate(expr)
		if everr != nil {
			fmt.Fprintln(os.Stderr, everr)
			os.Exit(70)
		}
		fmt.Println(stringifyValue(value))
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
	Line    int
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

	add := func(tokenType, lexeme, literal string) {
		tokens = append(tokens, Token{tokenType, lexeme, literal, line})
	}

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
			add("LEFT_PAREN", "(", "null")
		case ')':
			add("RIGHT_PAREN", ")", "null")
		case '{':
			add("LEFT_BRACE", "{", "null")
		case '}':
			add("RIGHT_BRACE", "}", "null")
		case ',':
			add("COMMA", ",", "null")
		case '.':
			add("DOT", ".", "null")
		case '-':
			add("MINUS", "-", "null")
		case '+':
			add("PLUS", "+", "null")
		case ';':
			add("SEMICOLON", ";", "null")
		case '*':
			add("STAR", "*", "null")
		case '/':
			if matchNext('/') {
				for i < len(source) && source[i] != '\n' {
					i++
				}
			} else {
				add("SLASH", "/", "null")
			}
		case '=':
			if matchNext('=') {
				add("EQUAL_EQUAL", "==", "null")
			} else {
				add("EQUAL", "=", "null")
			}
		case '!':
			if matchNext('=') {
				add("BANG_EQUAL", "!=", "null")
			} else {
				add("BANG", "!", "null")
			}
		case '<':
			if matchNext('=') {
				add("LESS_EQUAL", "<=", "null")
			} else {
				add("LESS", "<", "null")
			}
		case '>':
			if matchNext('=') {
				add("GREATER_EQUAL", ">=", "null")
			} else {
				add("GREATER", ">", "null")
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
				add("STRING", `"`+value+`"`, value)
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
				add("NUMBER", lexeme, formatASTLiteral(value))
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
				add(tokenType, text, "null")
			} else {
				errors = append(errors, fmt.Sprintf("[line %d] Error: Unexpected character: %c", line, c))
			}
		}
	}
	add("EOF", "", "null")
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
	Value interface{} // bool, float64, string, or nil
}

func (e LiteralExpr) String() string {
	return formatASTLiteral(e.Value)
}

// formatASTLiteral formats a literal value the way the "parse" command's
// AST printer does — notably, a whole-number float always gets a trailing
// ".0" (e.g. "2.0"), unlike the plainer formatting evaluate() output uses
// (formatLoxNumber alone, e.g. "2").
func formatASTLiteral(v interface{}) string {
	if f, ok := v.(float64); ok {
		s := formatLoxNumber(f)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return stringifyValue(v)
}

// formatLoxNumber formats a float64 the way Lox numbers print: the
// shortest decimal that round-trips, no forced trailing ".0".
func formatLoxNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// stringifyValue formats an evaluated Lox runtime value for the
// "evaluate" command's output.
func stringifyValue(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return "nil"
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		return formatLoxNumber(val)
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// GroupingExpr is a parenthesized sub-expression, e.g. "(foo)".
type GroupingExpr struct {
	Inner Expr
}

func (e GroupingExpr) String() string {
	return fmt.Sprintf("(group %s)", e.Inner.String())
}

// UnaryExpr is a prefix "!"/"-" applied to Right.
type UnaryExpr struct {
	Operator string
	Right    Expr
}

func (e UnaryExpr) String() string {
	return fmt.Sprintf("(%s %s)", e.Operator, e.Right.String())
}

// BinaryExpr is Left <op> Right.
type BinaryExpr struct {
	Operator string
	Left     Expr
	Right    Expr
}

func (e BinaryExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Operator, e.Left.String(), e.Right.String())
}

// Parser turns a token list into an Expr tree via recursive descent.
type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens}
}

func (p *Parser) parseExpression() (Expr, error) {
	return p.equality()
}

// binaryLevel implements one left-associative binary-operator precedence
// level: parse one operand via next, then keep consuming (operator,
// operand) pairs as long as the current token's type is one of types.
func (p *Parser) binaryLevel(next func() (Expr, error), types ...string) (Expr, error) {
	expr, err := next()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.tokens) {
		t := p.tokens[p.pos].Type
		matched := false
		for _, want := range types {
			if t == want {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		op := p.tokens[p.pos].Lexeme
		p.pos++
		right, err := next()
		if err != nil {
			return nil, err
		}
		expr = BinaryExpr{op, expr, right}
	}
	return expr, nil
}

// equality parses left-associative "=="/"!=", the loosest binary
// precedence level, above comparison.
func (p *Parser) equality() (Expr, error) {
	return p.binaryLevel(p.comparison, "EQUAL_EQUAL", "BANG_EQUAL")
}

// comparison parses left-associative "<"/"<="/">"/">=", one level looser
// than term.
func (p *Parser) comparison() (Expr, error) {
	return p.binaryLevel(p.term, "LESS", "LESS_EQUAL", "GREATER", "GREATER_EQUAL")
}

// term parses left-associative "+"/"-", one level looser than factor.
func (p *Parser) term() (Expr, error) {
	return p.binaryLevel(p.factor, "PLUS", "MINUS")
}

// factor parses left-associative "*"/"/" at the tightest binary precedence
// level, above unary.
func (p *Parser) factor() (Expr, error) {
	return p.binaryLevel(p.unary, "STAR", "SLASH")
}

// unary parses "!"/"-" prefix operators, which are right-associative
// (they recurse into another unary rather than falling straight to
// primary, so "!!true" parses as "(! (! true))").
func (p *Parser) unary() (Expr, error) {
	tok := p.tokens[p.pos]
	if tok.Type == "BANG" || tok.Type == "MINUS" {
		p.pos++
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		return UnaryExpr{tok.Lexeme, right}, nil
	}
	return p.primary()
}

func (p *Parser) primary() (Expr, error) {
	tok := p.tokens[p.pos]
	switch tok.Type {
	case "TRUE":
		p.pos++
		return LiteralExpr{true}, nil
	case "FALSE":
		p.pos++
		return LiteralExpr{false}, nil
	case "NIL":
		p.pos++
		return LiteralExpr{nil}, nil
	case "NUMBER":
		p.pos++
		value, _ := strconv.ParseFloat(tok.Literal, 64)
		return LiteralExpr{value}, nil
	case "STRING":
		p.pos++
		return LiteralExpr{tok.Literal}, nil
	case "LEFT_PAREN":
		p.pos++
		inner, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.pos < len(p.tokens) && p.tokens[p.pos].Type == "RIGHT_PAREN" {
			p.pos++
		}
		return GroupingExpr{inner}, nil
	}
	return nil, fmt.Errorf("[line %d] Error at %s: Expect expression.", tok.Line, describeToken(tok))
}

// describeToken formats a token for a parse error message: "'lexeme'", or
// "end" for EOF, matching the book's error format.
func describeToken(tok Token) string {
	if tok.Type == "EOF" {
		return "end"
	}
	return "'" + tok.Lexeme + "'"
}

// isTruthy applies Lox's truthiness rule: false and nil are falsy,
// everything else (including 0 and "") is truthy.
func isTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

// evaluate walks an Expr tree and returns its runtime value (bool,
// float64, string, or nil).
func evaluate(e Expr) (interface{}, error) {
	switch expr := e.(type) {
	case LiteralExpr:
		return expr.Value, nil
	case GroupingExpr:
		return evaluate(expr.Inner)
	case UnaryExpr:
		right, err := evaluate(expr.Right)
		if err != nil {
			return nil, err
		}
		switch expr.Operator {
		case "-":
			return -right.(float64), nil
		case "!":
			return !isTruthy(right), nil
		}
	case BinaryExpr:
		left, err := evaluate(expr.Left)
		if err != nil {
			return nil, err
		}
		right, err := evaluate(expr.Right)
		if err != nil {
			return nil, err
		}
		switch expr.Operator {
		case "*":
			return left.(float64) * right.(float64), nil
		case "/":
			return left.(float64) / right.(float64), nil
		case "+":
			return left.(float64) + right.(float64), nil
		case "-":
			return left.(float64) - right.(float64), nil
		}
	}
	return nil, fmt.Errorf("cannot evaluate expression of type %T", e)
}
