package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
		value, everr := evaluate(expr, NewEnvironment())
		if everr != nil {
			fmt.Fprintln(os.Stderr, everr)
			os.Exit(70)
		}
		fmt.Println(stringifyValue(value))
	case "run":
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		if len(errs) > 0 {
			os.Exit(65)
		}
		stmts, perr := NewParser(tokens).parseProgram()
		if perr != nil {
			fmt.Fprintln(os.Stderr, perr)
			os.Exit(65)
		}
		if rerr := NewResolver().resolveStmts(stmts); rerr != nil {
			fmt.Fprintln(os.Stderr, rerr)
			os.Exit(65)
		}
		env := NewEnvironment()
		for _, stmt := range stmts {
			if everr := execute(stmt, env); everr != nil {
				fmt.Fprintln(os.Stderr, everr)
				os.Exit(70)
			}
		}
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
	case *LoxFunction:
		return fmt.Sprintf("<fn %s>", val.Declaration.Name)
	case *LoxClass:
		return val.Name
	case *LoxInstance:
		return val.Class.Name + " instance"
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
	Line     int // operator's source line, for runtime error messages
}

func (e UnaryExpr) String() string {
	return fmt.Sprintf("(%s %s)", e.Operator, e.Right.String())
}

// BinaryExpr is Left <op> Right.
type BinaryExpr struct {
	Operator string
	Left     Expr
	Right    Expr
	Line     int // operator's source line, for runtime error messages
}

func (e BinaryExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Operator, e.Left.String(), e.Right.String())
}

// VariableExpr is a reference to a variable by name.
type VariableExpr struct {
	Name string
	Line int
}

func (e VariableExpr) String() string {
	return e.Name
}

// AssignExpr is "name = value", which itself evaluates to value (so
// "print quz = 2;" prints 2).
type AssignExpr struct {
	Name  string
	Value Expr
	Line  int
}

func (e AssignExpr) String() string {
	return fmt.Sprintf("(= %s %s)", e.Name, e.Value.String())
}

// LogicalExpr is "left or right" / "left and right" — unlike BinaryExpr,
// Right must NOT be evaluated eagerly, since or/and short-circuit.
type LogicalExpr struct {
	Operator string
	Left     Expr
	Right    Expr
}

func (e LogicalExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Operator, e.Left.String(), e.Right.String())
}

// CallExpr is "callee(arguments...)".
type CallExpr struct {
	Callee    Expr
	Arguments []Expr
	Line      int // the call's opening "(", for runtime error messages
}

func (e CallExpr) String() string {
	args := make([]string, len(e.Arguments))
	for i, a := range e.Arguments {
		args[i] = a.String()
	}
	return fmt.Sprintf("(call %s %s)", e.Callee.String(), strings.Join(args, " "))
}

// GetExpr is a property read, e.g. "object.name".
type GetExpr struct {
	Object Expr
	Name   string
	Line   int
}

func (e *GetExpr) String() string {
	return fmt.Sprintf("(get %s %s)", e.Object.String(), e.Name)
}

// SetExpr is a property write, e.g. "object.name = value".
type SetExpr struct {
	Object Expr
	Name   string
	Value  Expr
	Line   int
}

func (e *SetExpr) String() string {
	return fmt.Sprintf("(set %s %s %s)", e.Object.String(), e.Name, e.Value.String())
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
	return p.assignment()
}

// assignment parses "target = value", right-associative (so "a = b = 1"
// is "a = (b = 1)") and the loosest-binding form — everything else
// (equality and below) is parsed first, and only reinterpreted as an
// assignment target if a "=" follows and the parsed expression turns out
// to be a plain variable reference.
func (p *Parser) assignment() (Expr, error) {
	expr, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.tokens[p.pos].Type == "EQUAL" {
		eqTok := p.tokens[p.pos]
		p.pos++
		value, err := p.assignment()
		if err != nil {
			return nil, err
		}
		if target, ok := expr.(*VariableExpr); ok {
			return &AssignExpr{target.Name, value, eqTok.Line}, nil
		}
		if target, ok := expr.(*GetExpr); ok {
			return &SetExpr{Object: target.Object, Name: target.Name, Value: value, Line: eqTok.Line}, nil
		}
		return nil, fmt.Errorf("[line %d] Error at '=': Invalid assignment target.", eqTok.Line)
	}
	return expr, nil
}

// or parses left-associative "or", one level looser than equality (and
// binds looser than assignment, so "a = b or c" is "a = (b or c)").
func (p *Parser) or() (Expr, error) {
	expr, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.tokens[p.pos].Type == "OR" {
		p.pos++
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		expr = LogicalExpr{"or", expr, right}
	}
	return expr, nil
}

// and parses left-associative "and", one level tighter than or (so
// "and" binds tighter — "a or b and c" is "a or (b and c)").
func (p *Parser) and() (Expr, error) {
	expr, err := p.equality()
	if err != nil {
		return nil, err
	}
	for p.tokens[p.pos].Type == "AND" {
		p.pos++
		right, err := p.equality()
		if err != nil {
			return nil, err
		}
		expr = LogicalExpr{"and", expr, right}
	}
	return expr, nil
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
		opTok := p.tokens[p.pos]
		p.pos++
		right, err := next()
		if err != nil {
			return nil, err
		}
		expr = BinaryExpr{opTok.Lexeme, expr, right, opTok.Line}
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
		return UnaryExpr{tok.Lexeme, right, tok.Line}, nil
	}
	return p.call()
}

// call parses a primary expression followed by zero or more "(args...)"
// call suffixes, e.g. "clock()" or (once functions returning functions
// exist) "f()()".
func (p *Parser) call() (Expr, error) {
	expr, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		if p.tokens[p.pos].Type == "LEFT_PAREN" {
			parenTok := p.tokens[p.pos]
			p.pos++
			var args []Expr
			if p.tokens[p.pos].Type != "RIGHT_PAREN" {
				for {
					arg, err := p.parseExpression()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.tokens[p.pos].Type != "COMMA" {
						break
					}
					p.pos++
				}
			}
			if err := p.expectToken("RIGHT_PAREN", "')' after arguments"); err != nil {
				return nil, err
			}
			expr = CallExpr{expr, args, parenTok.Line}
		} else if p.tokens[p.pos].Type == "DOT" {
			p.pos++
			if p.tokens[p.pos].Type != "IDENTIFIER" {
				tok := p.tokens[p.pos]
				return nil, fmt.Errorf("[line %d] Error at %s: Expect property name after '.'.", tok.Line, describeToken(tok))
			}
			nameTok := p.tokens[p.pos]
			p.pos++
			expr = &GetExpr{Object: expr, Name: nameTok.Lexeme, Line: nameTok.Line}
		} else {
			break
		}
	}
	return expr, nil
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
		if p.tokens[p.pos].Type != "RIGHT_PAREN" {
			closeTok := p.tokens[p.pos]
			return nil, fmt.Errorf("[line %d] Error at %s: Expect ')' after expression.", closeTok.Line, describeToken(closeTok))
		}
		p.pos++
		return GroupingExpr{inner}, nil
	case "IDENTIFIER":
		p.pos++
		return &VariableExpr{tok.Lexeme, tok.Line}, nil
	case "THIS":
		p.pos++
		return &VariableExpr{tok.Lexeme, tok.Line}, nil
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

// numberOperands returns left/right as float64 and true if both are
// numbers, or zero values and false otherwise.
func numberOperands(left, right interface{}) (float64, float64, bool) {
	l, ok1 := left.(float64)
	r, ok2 := right.(float64)
	return l, r, ok1 && ok2
}

// RuntimeError is a Lox runtime error (as opposed to a parse-time syntax
// error), reported as "<message>\n[line N]" per the book's format.
type RuntimeError struct {
	Message string
	Line    int
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("%s\n[line %d]", e.Message, e.Line)
}

// locals maps a *VariableExpr or *AssignExpr node (by pointer identity —
// each occurrence in the source is a distinct allocation, even if two
// occurrences share the same name and line) to how many Environment
// parent-hops away its declaring scope is, as computed by a Resolver
// pass before execution. A node absent from this map is assumed global,
// looked up dynamically via Environment.Get instead.
var locals = map[interface{}]int{}

// Resolver performs a static pass over the AST before execution, so a
// closure binds to the specific variable that existed in its enclosing
// scope at declaration time — a plain dynamic environment lookup by name
// would instead see whatever's in that scope by the time the closure is
// actually called, which is wrong if the same name gets redeclared in
// between (see the "shouldn't affect the usage in f above" test case).
type Resolver struct {
	scopes          []map[string]bool // scope stack; value is whether the name is fully defined yet
	inFunctionDepth     int    // >0 while resolving a function body, for "return outside function" detection
	inClassDepth        int    // >0 while resolving a class's methods, for "this outside a class" detection
	currentFunctionKind string // kind of the innermost function being resolved: "function", "method", or "initializer"
}

func NewResolver() *Resolver {
	return &Resolver{}
}

func (r *Resolver) beginScope() {
	r.scopes = append(r.scopes, map[string]bool{})
}

func (r *Resolver) endScope() {
	r.scopes = r.scopes[:len(r.scopes)-1]
}

func (r *Resolver) declare(name string, line int) error {
	if len(r.scopes) == 0 {
		return nil
	}
	if _, ok := r.scopes[len(r.scopes)-1][name]; ok {
		return fmt.Errorf("[line %d] Error at '%s': Already a variable with this name in this scope.", line, name)
	}
	r.scopes[len(r.scopes)-1][name] = false
	return nil
}

func (r *Resolver) define(name string) {
	if len(r.scopes) > 0 {
		r.scopes[len(r.scopes)-1][name] = true
	}
}

// resolveLocal records how many scopes out name is declared, or leaves
// node unresolved (assumed global) if it isn't found in any local scope.
func (r *Resolver) resolveLocal(node interface{}, name string) {
	for i := len(r.scopes) - 1; i >= 0; i-- {
		if _, ok := r.scopes[i][name]; ok {
			locals[node] = len(r.scopes) - 1 - i
			return
		}
	}
}

func (r *Resolver) resolveStmts(stmts []Stmt) error {
	for _, s := range stmts {
		if err := r.resolveStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) resolveStmt(stmt Stmt) error {
	switch s := stmt.(type) {
	case BlockStmt:
		r.beginScope()
		err := r.resolveStmts(s.Statements)
		r.endScope()
		return err
	case VarStmt:
		if err := r.declare(s.Name, s.Line); err != nil {
			return err
		}
		if s.Initializer != nil {
			if err := r.resolveExpr(s.Initializer); err != nil {
				return err
			}
		}
		r.define(s.Name)
		return nil
	case FunctionStmt:
		if err := r.declare(s.Name, s.Line); err != nil {
			return err
		}
		r.define(s.Name)
		return r.resolveFunction(s, "function")
	case ClassStmt:
		if err := r.declare(s.Name, s.Line); err != nil {
			return err
		}
		r.define(s.Name)
		r.inClassDepth++
		r.beginScope()
		r.scopes[len(r.scopes)-1]["this"] = true
		for _, method := range s.Methods {
			kind := "method"
			if method.Name == "init" {
				kind = "initializer"
			}
			if err := r.resolveFunction(method, kind); err != nil {
				r.endScope()
				r.inClassDepth--
				return err
			}
		}
		r.endScope()
		r.inClassDepth--
		return nil
	case ExprStmt:
		return r.resolveExpr(s.Expr)
	case PrintStmt:
		return r.resolveExpr(s.Expr)
	case ReturnStmt:
		if r.inFunctionDepth == 0 {
			return fmt.Errorf("[line %d] Error at 'return': Can't return from top-level code.", s.Line)
		}
		if s.Value != nil {
			if r.currentFunctionKind == "initializer" {
				return fmt.Errorf("[line %d] Error at 'return': Can't return a value from an initializer.", s.Line)
			}
			return r.resolveExpr(s.Value)
		}
		return nil
	case IfStmt:
		if err := r.resolveExpr(s.Condition); err != nil {
			return err
		}
		if err := r.resolveStmt(s.ThenBranch); err != nil {
			return err
		}
		if s.ElseBranch != nil {
			return r.resolveStmt(s.ElseBranch)
		}
		return nil
	case WhileStmt:
		if err := r.resolveExpr(s.Condition); err != nil {
			return err
		}
		return r.resolveStmt(s.Body)
	}
	return nil
}

func (r *Resolver) resolveFunction(s FunctionStmt, kind string) error {
	enclosingKind := r.currentFunctionKind
	r.currentFunctionKind = kind
	r.inFunctionDepth++
	r.beginScope()
	for _, param := range s.Params {
		if err := r.declare(param.Name, param.Line); err != nil {
			r.endScope()
			r.inFunctionDepth--
			r.currentFunctionKind = enclosingKind
			return err
		}
		r.define(param.Name)
	}
	err := r.resolveStmts(s.Body)
	r.endScope()
	r.inFunctionDepth--
	r.currentFunctionKind = enclosingKind
	return err
}

func (r *Resolver) resolveExpr(expr Expr) error {
	switch e := expr.(type) {
	case *VariableExpr:
		if e.Name == "this" && r.inClassDepth == 0 {
			return fmt.Errorf("[line %d] Error at 'this': Can't use 'this' outside of a class.", e.Line)
		}
		if len(r.scopes) > 0 {
			if defined, ok := r.scopes[len(r.scopes)-1][e.Name]; ok && !defined {
				return fmt.Errorf("[line %d] Error at '%s': Can't read local variable in its own initializer.", e.Line, e.Name)
			}
		}
		r.resolveLocal(e, e.Name)
		return nil
	case *AssignExpr:
		if err := r.resolveExpr(e.Value); err != nil {
			return err
		}
		r.resolveLocal(e, e.Name)
		return nil
	case BinaryExpr:
		if err := r.resolveExpr(e.Left); err != nil {
			return err
		}
		return r.resolveExpr(e.Right)
	case LogicalExpr:
		if err := r.resolveExpr(e.Left); err != nil {
			return err
		}
		return r.resolveExpr(e.Right)
	case UnaryExpr:
		return r.resolveExpr(e.Right)
	case GroupingExpr:
		return r.resolveExpr(e.Inner)
	case CallExpr:
		if err := r.resolveExpr(e.Callee); err != nil {
			return err
		}
		for _, a := range e.Arguments {
			if err := r.resolveExpr(a); err != nil {
				return err
			}
		}
		return nil
	case *GetExpr:
		return r.resolveExpr(e.Object)
	case *SetExpr:
		if err := r.resolveExpr(e.Value); err != nil {
			return err
		}
		return r.resolveExpr(e.Object)
	}
	return nil
}

// evaluate walks an Expr tree and returns its runtime value (bool,
// float64, string, or nil).
func evaluate(e Expr, env *Environment) (interface{}, error) {
	switch expr := e.(type) {
	case LiteralExpr:
		return expr.Value, nil
	case GroupingExpr:
		return evaluate(expr.Inner, env)
	case UnaryExpr:
		right, err := evaluate(expr.Right, env)
		if err != nil {
			return nil, err
		}
		switch expr.Operator {
		case "-":
			n, ok := right.(float64)
			if !ok {
				return nil, &RuntimeError{"Operand must be a number.", expr.Line}
			}
			return -n, nil
		case "!":
			return !isTruthy(right), nil
		}
	case BinaryExpr:
		left, err := evaluate(expr.Left, env)
		if err != nil {
			return nil, err
		}
		right, err := evaluate(expr.Right, env)
		if err != nil {
			return nil, err
		}
		switch expr.Operator {
		case "*":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l * r, nil
		case "/":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l / r, nil
		case "+":
			if leftStr, ok := left.(string); ok {
				if rightStr, ok := right.(string); ok {
					return leftStr + rightStr, nil
				}
			} else if l, r, ok := numberOperands(left, right); ok {
				return l + r, nil
			}
			return nil, &RuntimeError{"Operands must be two numbers or two strings.", expr.Line}
		case "-":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l - r, nil
		case ">":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l > r, nil
		case "<":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l < r, nil
		case ">=":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l >= r, nil
		case "<=":
			l, r, ok := numberOperands(left, right)
			if !ok {
				return nil, &RuntimeError{"Operands must be numbers.", expr.Line}
			}
			return l <= r, nil
		case "==":
			return left == right, nil
		case "!=":
			return left != right, nil
		}
	case *VariableExpr:
		if distance, ok := locals[expr]; ok {
			return env.GetAt(distance, expr.Name), nil
		}
		return globalEnv.Get(expr.Name, expr.Line)
	case *AssignExpr:
		value, err := evaluate(expr.Value, env)
		if err != nil {
			return nil, err
		}
		if distance, ok := locals[expr]; ok {
			env.AssignAt(distance, expr.Name, value)
		} else if err := globalEnv.Assign(expr.Name, value, expr.Line); err != nil {
			return nil, err
		}
		return value, nil
	case LogicalExpr:
		left, err := evaluate(expr.Left, env)
		if err != nil {
			return nil, err
		}
		if expr.Operator == "or" {
			if isTruthy(left) {
				return left, nil
			}
		} else if !isTruthy(left) {
			return left, nil
		}
		return evaluate(expr.Right, env)
	case CallExpr:
		callee, err := evaluate(expr.Callee, env)
		if err != nil {
			return nil, err
		}
		callable, ok := callee.(LoxCallable)
		if !ok {
			return nil, &RuntimeError{"Can only call functions and classes.", expr.Line}
		}
		args := make([]interface{}, len(expr.Arguments))
		for i, argExpr := range expr.Arguments {
			args[i], err = evaluate(argExpr, env)
			if err != nil {
				return nil, err
			}
		}
		if len(args) != callable.Arity() {
			return nil, &RuntimeError{fmt.Sprintf("Expected %d arguments but got %d.", callable.Arity(), len(args)), expr.Line}
		}
		return callable.Call(args)
	case *GetExpr:
		object, err := evaluate(expr.Object, env)
		if err != nil {
			return nil, err
		}
		instance, ok := object.(*LoxInstance)
		if !ok {
			return nil, &RuntimeError{"Only instances have properties.", expr.Line}
		}
		return instance.Get(expr.Name, expr.Line)
	case *SetExpr:
		object, err := evaluate(expr.Object, env)
		if err != nil {
			return nil, err
		}
		instance, ok := object.(*LoxInstance)
		if !ok {
			return nil, &RuntimeError{"Only instances have fields.", expr.Line}
		}
		value, err := evaluate(expr.Value, env)
		if err != nil {
			return nil, err
		}
		instance.Set(expr.Name, value)
		return value, nil
	}
	return nil, fmt.Errorf("cannot evaluate expression of type %T", e)
}

// Environment holds variable bindings for one scope, chained to its
// enclosing scope's Environment (nil for the global scope) so lookups and
// assignments can walk outward through nested blocks.
type Environment struct {
	values map[string]interface{}
	parent *Environment
}

var globalEnv *Environment

func NewEnvironment() *Environment {
	env := &Environment{values: map[string]interface{}{}}
	env.Define("clock", &NativeFunction{
		arity: 0,
		fn: func(args []interface{}) (interface{}, error) {
			return float64(time.Now().Unix()), nil
		},
	})
	globalEnv = env
	return env
}

// LoxCallable is anything invocable with call syntax: native functions now,
// user-defined functions in a later stage.
type LoxCallable interface {
	Arity() int
	Call(args []interface{}) (interface{}, error)
}

// NativeFunction wraps a Go function as a LoxCallable, for builtins like
// clock() that aren't written in Lox itself.
type NativeFunction struct {
	arity int
	fn    func(args []interface{}) (interface{}, error)
}

func (n *NativeFunction) Arity() int { return n.arity }
func (n *NativeFunction) Call(args []interface{}) (interface{}, error) {
	return n.fn(args)
}

// LoxFunction is a user-defined ("fun") function: its declaration plus
// the environment it closed over at definition time.
type LoxFunction struct {
	Declaration   FunctionStmt
	Closure       *Environment
	IsInitializer bool // true for a class's "init" method: always returns "this"
}

func (f *LoxFunction) Arity() int { return len(f.Declaration.Params) }

// Bind returns a copy of f whose closure has "this" bound to instance,
// used when a method is accessed off an instance.
func (f *LoxFunction) Bind(instance *LoxInstance) *LoxFunction {
	env := NewChildEnvironment(f.Closure)
	env.Define("this", instance)
	return &LoxFunction{Declaration: f.Declaration, Closure: env, IsInitializer: f.IsInitializer}
}

func (f *LoxFunction) Call(args []interface{}) (interface{}, error) {
	callEnv := NewChildEnvironment(f.Closure)
	for i, param := range f.Declaration.Params {
		callEnv.Define(param.Name, args[i])
	}
	for _, stmt := range f.Declaration.Body {
		err := execute(stmt, callEnv)
		if err == nil {
			continue
		}
		if ret, ok := err.(*returnSignal); ok {
			if f.IsInitializer {
				return f.Closure.Get("this", 0)
			}
			return ret.Value, nil
		}
		return nil, err
	}
	if f.IsInitializer {
		return f.Closure.Get("this", 0)
	}
	return nil, nil
}

// returnSignal is how a "return" statement unwinds out of however many
// nested statements/blocks it's inside, back up to the enclosing
// LoxFunction.Call — abusing Go's error-propagation plumbing (every
// execute() call already checks and forwards errors) as ad hoc
// non-local control flow, the same way the book's Java implementation
// uses an exception for this.
type returnSignal struct {
	Value interface{}
}

func (r *returnSignal) Error() string {
	return "return outside a function"
}

// NewChildEnvironment creates a new innermost scope enclosed by parent,
// used for the duration of one block statement.
func NewChildEnvironment(parent *Environment) *Environment {
	return &Environment{values: map[string]interface{}{}, parent: parent}
}

// Define always declares in this scope, even if an outer scope already
// has a variable of the same name (shadowing, not redeclaration).
func (env *Environment) Define(name string, value interface{}) {
	env.values[name] = value
}

func (env *Environment) Get(name string, line int) (interface{}, error) {
	if v, ok := env.values[name]; ok {
		return v, nil
	}
	if env.parent != nil {
		return env.parent.Get(name, line)
	}
	return nil, &RuntimeError{fmt.Sprintf("Undefined variable '%s'.", name), line}
}

// Assign sets an already-declared variable's value, walking outward
// through enclosing scopes to find where it was declared. Errors if it
// was never declared anywhere in the chain (assignment doesn't implicitly
// declare, unlike Define).
func (env *Environment) Assign(name string, value interface{}, line int) error {
	if _, ok := env.values[name]; ok {
		env.values[name] = value
		return nil
	}
	if env.parent != nil {
		return env.parent.Assign(name, value, line)
	}
	return &RuntimeError{fmt.Sprintf("Undefined variable '%s'.", name), line}
}

// ancestor walks exactly distance parents up from env — used with a
// resolver-computed distance, where (unlike Get/Assign's dynamic search)
// the variable is guaranteed to exist at exactly that scope.
func (env *Environment) ancestor(distance int) *Environment {
	e := env
	for i := 0; i < distance; i++ {
		e = e.parent
	}
	return e
}

func (env *Environment) GetAt(distance int, name string) interface{} {
	return env.ancestor(distance).values[name]
}

func (env *Environment) AssignAt(distance int, name string, value interface{}) {
	env.ancestor(distance).values[name] = value
}

// Stmt is a parsed Lox statement, executed by the "run" command.
type Stmt interface{}

// PrintStmt is "print <expr>;".
type PrintStmt struct {
	Expr Expr
}

// VarStmt is "var <name> [= <initializer>];".
type VarStmt struct {
	Name        string
	Initializer Expr // nil if no initializer
	Line        int
}

// ExprStmt is a bare expression, evaluated for its side effects and
// otherwise discarded.
type ExprStmt struct {
	Expr Expr
}

// BlockStmt is "{ <statements> }".
type BlockStmt struct {
	Statements []Stmt
}

// IfStmt is "if (condition) thenBranch [else elseBranch]". ElseBranch is
// nil if there's no else clause.
type IfStmt struct {
	Condition  Expr
	ThenBranch Stmt
	ElseBranch Stmt
}

// WhileStmt is "while (condition) body".
type WhileStmt struct {
	Condition Expr
	Body      Stmt
}

// Param is a function parameter name paired with the line it was
// declared on, for resolver error reporting.
type Param struct {
	Name string
	Line int
}

// FunctionStmt is "fun name(params...) { body }".
type FunctionStmt struct {
	Name   string
	Params []Param
	Body   []Stmt
	Line   int
}

// ReturnStmt is "return [value];". Value is nil for a bare "return;".
type ReturnStmt struct {
	Value Expr
	Line  int
}

// ClassStmt is "class name { methods... }".
type ClassStmt struct {
	Name    string
	Methods []FunctionStmt
	Line    int
}

// LoxClass is a class value: printable and callable to construct instances.
type LoxClass struct {
	Name    string
	Methods map[string]FunctionStmt
	Closure *Environment // environment the class was declared in, for binding methods
}

func (c *LoxClass) FindMethod(name string) (FunctionStmt, bool) {
	method, ok := c.Methods[name]
	return method, ok
}

func (c *LoxClass) Arity() int {
	if init, ok := c.FindMethod("init"); ok {
		return len(init.Params)
	}
	return 0
}

func (c *LoxClass) Call(args []interface{}) (interface{}, error) {
	instance := &LoxInstance{Class: c}
	if init, ok := c.FindMethod("init"); ok {
		fn := &LoxFunction{Declaration: init, Closure: c.Closure, IsInitializer: true}
		if _, err := fn.Bind(instance).Call(args); err != nil {
			return nil, err
		}
	}
	return instance, nil
}

// LoxInstance is an instance of a LoxClass.
type LoxInstance struct {
	Class  *LoxClass
	Fields map[string]interface{}
}

func (i *LoxInstance) Get(name string, line int) (interface{}, error) {
	if value, ok := i.Fields[name]; ok {
		return value, nil
	}
	if method, ok := i.Class.FindMethod(name); ok {
		fn := &LoxFunction{Declaration: method, Closure: i.Class.Closure, IsInitializer: method.Name == "init"}
		return fn.Bind(i), nil
	}
	return nil, &RuntimeError{fmt.Sprintf("Undefined property '%s'.", name), line}
}

func (i *LoxInstance) Set(name string, value interface{}) {
	if i.Fields == nil {
		i.Fields = map[string]interface{}{}
	}
	i.Fields[name] = value
}

// parseProgram parses a full "run"-mode source as a sequence of
// semicolon-terminated statements, up to EOF.
func (p *Parser) parseProgram() ([]Stmt, error) {
	var stmts []Stmt
	for p.pos < len(p.tokens) && p.tokens[p.pos].Type != "EOF" {
		stmt, err := p.parseDeclaration()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
	}
	return stmts, nil
}

// parseDeclaration parses a "var" declaration or, failing that, falls
// back to parseStatement. This distinction matters: a bare "var" is only
// allowed directly at the top level or as a block's own content, NOT as
// the single-statement body of an if/while/for (e.g. "for (;;) var x;"
// is a syntax error in real Lox) — so callers that parse such a body use
// parseStatement directly instead of going through here.
func (p *Parser) parseDeclaration() (Stmt, error) {
	if p.tokens[p.pos].Type == "VAR" {
		return p.parseVarDecl()
	}
	if p.tokens[p.pos].Type == "FUN" {
		return p.parseFunctionDecl()
	}
	if p.tokens[p.pos].Type == "CLASS" {
		return p.parseClassDecl()
	}
	return p.parseStatement()
}

// parseClassDecl parses "class name { }".
func (p *Parser) parseClassDecl() (Stmt, error) {
	p.pos++ // consume "class"
	if p.tokens[p.pos].Type != "IDENTIFIER" {
		tok := p.tokens[p.pos]
		return nil, fmt.Errorf("[line %d] Error at %s: Expect class name.", tok.Line, describeToken(tok))
	}
	nameTok := p.tokens[p.pos]
	p.pos++
	if err := p.expectToken("LEFT_BRACE", "'{' before class body"); err != nil {
		return nil, err
	}
	var methods []FunctionStmt
	for p.tokens[p.pos].Type != "RIGHT_BRACE" && p.tokens[p.pos].Type != "EOF" {
		if p.tokens[p.pos].Type != "IDENTIFIER" {
			tok := p.tokens[p.pos]
			return nil, fmt.Errorf("[line %d] Error at %s: Expect method name.", tok.Line, describeToken(tok))
		}
		methodNameTok := p.tokens[p.pos]
		p.pos++
		method, err := p.parseFunctionBody(methodNameTok, "method")
		if err != nil {
			return nil, err
		}
		methods = append(methods, method.(FunctionStmt))
	}
	if err := p.expectToken("RIGHT_BRACE", "'}' after class body"); err != nil {
		return nil, err
	}
	return ClassStmt{Name: nameTok.Lexeme, Methods: methods, Line: nameTok.Line}, nil
}

// parseBlockBody parses statements up to (and consuming) a closing "}",
// assuming the opening "{" was already consumed by the caller. Shared by
// block statements and function bodies.
func (p *Parser) parseBlockBody() ([]Stmt, error) {
	var stmts []Stmt
	for p.tokens[p.pos].Type != "RIGHT_BRACE" && p.tokens[p.pos].Type != "EOF" {
		stmt, err := p.parseDeclaration()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
	}
	if p.tokens[p.pos].Type != "RIGHT_BRACE" {
		tok := p.tokens[p.pos]
		return nil, fmt.Errorf("[line %d] Error at %s: Expect '}' .", tok.Line, describeToken(tok))
	}
	p.pos++
	return stmts, nil
}

// parseFunctionDecl parses "fun name() { body }".
func (p *Parser) parseFunctionDecl() (Stmt, error) {
	p.pos++ // consume "fun"
	if p.tokens[p.pos].Type != "IDENTIFIER" {
		tok := p.tokens[p.pos]
		return nil, fmt.Errorf("[line %d] Error at %s: Expect function name.", tok.Line, describeToken(tok))
	}
	nameTok := p.tokens[p.pos]
	p.pos++
	return p.parseFunctionBody(nameTok, "function")
}

// parseFunctionBody parses "(params...) { body }", shared by top-level
// function declarations and method declarations inside a class body
// (which don't have a leading "fun" keyword or already-consumed name).
func (p *Parser) parseFunctionBody(nameTok Token, kind string) (Stmt, error) {
	name := nameTok.Lexeme
	if err := p.expectToken("LEFT_PAREN", fmt.Sprintf("'(' after %s name", kind)); err != nil {
		return nil, err
	}
	var params []Param
	if p.tokens[p.pos].Type != "RIGHT_PAREN" {
		for {
			if p.tokens[p.pos].Type != "IDENTIFIER" {
				tok := p.tokens[p.pos]
				return nil, fmt.Errorf("[line %d] Error at %s: Expect parameter name.", tok.Line, describeToken(tok))
			}
			params = append(params, Param{Name: p.tokens[p.pos].Lexeme, Line: p.tokens[p.pos].Line})
			p.pos++
			if p.tokens[p.pos].Type != "COMMA" {
				break
			}
			p.pos++
		}
	}
	if err := p.expectToken("RIGHT_PAREN", "')' after parameters"); err != nil {
		return nil, err
	}
	if err := p.expectToken("LEFT_BRACE", fmt.Sprintf("'{' before %s body", kind)); err != nil {
		return nil, err
	}
	body, err := p.parseBlockBody()
	if err != nil {
		return nil, err
	}
	return FunctionStmt{Name: name, Params: params, Body: body, Line: nameTok.Line}, nil
}

// parseVarDecl parses "var name [= initializer];", used both directly as
// a statement and as a for-loop's initializer clause.
func (p *Parser) parseVarDecl() (Stmt, error) {
	p.pos++ // consume "var"
	if p.tokens[p.pos].Type != "IDENTIFIER" {
		tok := p.tokens[p.pos]
		return nil, fmt.Errorf("[line %d] Error at %s: Expect variable name.", tok.Line, describeToken(tok))
	}
	nameTok := p.tokens[p.pos]
	name := nameTok.Lexeme
	p.pos++
	var initializer Expr
	if p.tokens[p.pos].Type == "EQUAL" {
		p.pos++
		var err error
		initializer, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectSemicolon(); err != nil {
		return nil, err
	}
	return VarStmt{Name: name, Initializer: initializer, Line: nameTok.Line}, nil
}

func (p *Parser) parseStatement() (Stmt, error) {
	if p.tokens[p.pos].Type == "RETURN" {
		retTok := p.tokens[p.pos]
		p.pos++
		var value Expr
		if p.tokens[p.pos].Type != "SEMICOLON" {
			var err error
			value, err = p.parseExpression()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectSemicolon(); err != nil {
			return nil, err
		}
		return ReturnStmt{value, retTok.Line}, nil
	}
	if p.tokens[p.pos].Type == "FOR" {
		p.pos++
		if err := p.expectToken("LEFT_PAREN", "'(' after 'for'"); err != nil {
			return nil, err
		}

		var initializer Stmt
		var err error
		switch p.tokens[p.pos].Type {
		case "SEMICOLON":
			p.pos++
		case "VAR":
			initializer, err = p.parseVarDecl()
			if err != nil {
				return nil, err
			}
		default:
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if err := p.expectSemicolon(); err != nil {
				return nil, err
			}
			initializer = ExprStmt{expr}
		}

		var condition Expr
		if p.tokens[p.pos].Type != "SEMICOLON" {
			condition, err = p.parseExpression()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectSemicolon(); err != nil {
			return nil, err
		}

		var increment Expr
		if p.tokens[p.pos].Type != "RIGHT_PAREN" {
			increment, err = p.parseExpression()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expectToken("RIGHT_PAREN", "')' after for clauses"); err != nil {
			return nil, err
		}

		body, err := p.parseStatement()
		if err != nil {
			return nil, err
		}

		// Desugar for(init; cond; incr) body into:
		//   { init; while (cond) { body; incr; } }
		// — no dedicated ForStmt AST node needed.
		if increment != nil {
			body = BlockStmt{[]Stmt{body, ExprStmt{increment}}}
		}
		if condition == nil {
			condition = LiteralExpr{true}
		}
		body = WhileStmt{condition, body}
		if initializer != nil {
			body = BlockStmt{[]Stmt{initializer, body}}
		}
		return body, nil
	}
	if p.tokens[p.pos].Type == "WHILE" {
		p.pos++
		if err := p.expectToken("LEFT_PAREN", "'(' after 'while'"); err != nil {
			return nil, err
		}
		condition, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.expectToken("RIGHT_PAREN", "')' after condition"); err != nil {
			return nil, err
		}
		body, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		return WhileStmt{condition, body}, nil
	}
	if p.tokens[p.pos].Type == "IF" {
		p.pos++
		if err := p.expectToken("LEFT_PAREN", "'(' after 'if'"); err != nil {
			return nil, err
		}
		condition, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.expectToken("RIGHT_PAREN", "')' after if condition"); err != nil {
			return nil, err
		}
		thenBranch, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		var elseBranch Stmt
		if p.tokens[p.pos].Type == "ELSE" {
			p.pos++
			elseBranch, err = p.parseStatement()
			if err != nil {
				return nil, err
			}
		}
		return IfStmt{condition, thenBranch, elseBranch}, nil
	}
	if p.tokens[p.pos].Type == "LEFT_BRACE" {
		p.pos++
		stmts, err := p.parseBlockBody()
		if err != nil {
			return nil, err
		}
		return BlockStmt{stmts}, nil
	}
	if p.tokens[p.pos].Type == "PRINT" {
		p.pos++
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.expectSemicolon(); err != nil {
			return nil, err
		}
		return PrintStmt{expr}, nil
	}
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.expectSemicolon(); err != nil {
		return nil, err
	}
	return ExprStmt{expr}, nil
}

// expectSemicolon consumes a trailing ";" or returns a syntax error
// pointing at whatever token is there instead.
func (p *Parser) expectSemicolon() error {
	if p.pos < len(p.tokens) && p.tokens[p.pos].Type == "SEMICOLON" {
		p.pos++
		return nil
	}
	tok := p.tokens[p.pos]
	return fmt.Errorf("[line %d] Error at %s: Expect ';' after value.", tok.Line, describeToken(tok))
}

// expectToken consumes the current token if it has type wantType, or
// returns a syntax error naming what was expected ("Expect <what>.").
func (p *Parser) expectToken(wantType, what string) error {
	if p.pos < len(p.tokens) && p.tokens[p.pos].Type == wantType {
		p.pos++
		return nil
	}
	tok := p.tokens[p.pos]
	return fmt.Errorf("[line %d] Error at %s: Expect %s.", tok.Line, describeToken(tok), what)
}

// execute runs one statement, evaluating any expressions it contains.
func execute(stmt Stmt, env *Environment) error {
	switch s := stmt.(type) {
	case PrintStmt:
		value, err := evaluate(s.Expr, env)
		if err != nil {
			return err
		}
		fmt.Println(stringifyValue(value))
		return nil
	case VarStmt:
		var value interface{}
		if s.Initializer != nil {
			var err error
			value, err = evaluate(s.Initializer, env)
			if err != nil {
				return err
			}
		}
		env.Define(s.Name, value)
		return nil
	case ExprStmt:
		_, err := evaluate(s.Expr, env)
		return err
	case BlockStmt:
		blockEnv := NewChildEnvironment(env)
		for _, stmt := range s.Statements {
			if err := execute(stmt, blockEnv); err != nil {
				return err
			}
		}
		return nil
	case IfStmt:
		cond, err := evaluate(s.Condition, env)
		if err != nil {
			return err
		}
		if isTruthy(cond) {
			return execute(s.ThenBranch, env)
		} else if s.ElseBranch != nil {
			return execute(s.ElseBranch, env)
		}
		return nil
	case WhileStmt:
		for {
			cond, err := evaluate(s.Condition, env)
			if err != nil {
				return err
			}
			if !isTruthy(cond) {
				return nil
			}
			if err := execute(s.Body, env); err != nil {
				return err
			}
		}
	case FunctionStmt:
		env.Define(s.Name, &LoxFunction{Declaration: s, Closure: env})
		return nil
	case ClassStmt:
		methods := map[string]FunctionStmt{}
		for _, method := range s.Methods {
			methods[method.Name] = method
		}
		env.Define(s.Name, &LoxClass{Name: s.Name, Methods: methods, Closure: env})
		return nil
	case ReturnStmt:
		var value interface{}
		if s.Value != nil {
			var err error
			value, err = evaluate(s.Value, env)
			if err != nil {
				return err
			}
		}
		return &returnSignal{value}
	}
	return fmt.Errorf("cannot execute statement of type %T", stmt)
}
