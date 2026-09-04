---
title: Build your own Interpreter — primer
concepts: [lexing, recursive descent, ast, environments, closures]
---

## The mental model

An interpreter is a pipeline: **characters → tokens → tree → values.** Each
stage of the course extends exactly one of those boxes, and the boundaries hold
all the way to classes and inheritance. The language is Lox, from *Crafting
Interpreters*; you are building the tree-walking version.

Keep the phases genuinely separate. The scanner should not know what an
expression is, and the parser should not evaluate anything — the temptation to
shortcut appears around stage 20 and costs you at stage 60.

## Scanning

One pass over the source producing tokens: type, lexeme, literal value, line.
The only real subtleties are lookahead (`=` vs `==`, `/` vs a comment) and
tracking line numbers for errors. Lox reports errors to stderr with a specific
format and exits `65` — the tester checks that, so build error reporting in from
the first stage rather than retrofitting it.

## Parsing by precedence

Recursive descent turns the grammar into functions, one per precedence level,
each calling the next tighter one:

```
expression → assignment → or → and → equality → comparison
           → term → factor → unary → call → primary
```

Left-associativity is a `for` loop inside the level; right-associativity is
recursion into itself. Grouping recurses back to the top. This is why the
grammar's shape *is* the code's shape — and why adding an operator later is a
one-function change.

Statements are a separate family (`print`, `var`, block, `if`, `while`, `for`,
`fun`, `return`, `class`), and `for` is best desugared into a `while` rather
than given its own evaluation path.

## Evaluating

A tree walk over the AST. Lox is dynamically typed, so operators check types at
runtime and throw a runtime error (exit `70`) on mismatch — `+` is the fun one,
since it means both numeric addition and string concatenation.

Truthiness in Lox is narrow: only `nil` and `false` are falsey. `0` and `""` are
true.

## Environments and closures

Scope is a chain of environments, each with a pointer to its enclosing one;
lookup walks the chain. A function value captures the environment it was
*defined* in, and that is the whole of closures.

But naive chain-walking gets variable resolution wrong in one specific case —
a closure that captures a variable which is later shadowed in the same block
resolves to the wrong binding. The fix is a **resolver**: a static pass between
parse and evaluate that computes, for each variable use, how many environments
up it lives, so evaluation does a fixed number of hops instead of a search. It
is the one place the course asks you to add a phase rather than extend one.

## Classes

Classes are environments with conventions: a class object that can be called to
construct an instance, instances holding a field map, methods as functions bound
to `this` (a hidden environment holding the instance), `init` treated specially
on construction, and `super` resolved through the resolver as a lookup starting
one class higher.

## The stage arc

Scanning (tokens, strings, numbers, identifiers, keywords) → parsing
(literals, grouping, unary, binary, precedence) → evaluating → statements and
state → control flow → functions and closures → resolving → classes →
inheritance.

## Going deeper

- [*Crafting Interpreters* — the book this course follows](https://craftinginterpreters.com/)
- [Resolving and binding](https://craftinginterpreters.com/resolving-and-binding.html)
- [Pratt parsing, for the precedence idea](https://journal.stuffwithstuff.com/2011/03/19/pratt-parsers-expression-parsing-made-easy/)
