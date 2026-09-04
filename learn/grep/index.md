---
title: Build your own grep — primer
concepts: [regular expressions, backtracking, recursion, parsing]
---

## The mental model

A regex engine answers one question — *does this pattern match here?* — and
answers it recursively: match the first element of the pattern against the
current position, then ask the same question about the rest of the pattern at
the new position. Every feature in the course is a new kind of "first element".

You are writing a **backtracking** matcher, the same family as Perl and PCRE.
It can do things Go's own `regexp` cannot (backreferences), and it can go
exponential on pathological patterns — that trade is the lesson.

## The shape of the matcher

Two mutually recursive ideas carry the whole engine:

```
matchHere(pattern, text) bool   // does pattern match at exactly this position?
match(pattern, text)     bool   // does it match at any position?
```

`match` slides the start position unless the pattern is anchored; `matchHere`
consumes one element and recurses on the remainder. Once that skeleton is
right, each stage adds a case.

## The elements, roughly in course order

| Pattern | Meaning | What it adds to the matcher |
|---|---|---|
| `a` | literal | byte comparison |
| `\d` `\w` | digit / word class | a predicate instead of a byte |
| `[abc]` `[^abc]` | character group | a set, plus negation |
| `^` `$` | anchors | position checks, not consumption |
| `+` `?` | quantifiers | try longest first, then backtrack |
| `.` | wildcard | a predicate that always passes |
| `(a\|b)` | alternation | try each branch, recurse on the rest |
| `(…)` `\1` | groups, backreferences | capture state during the match |

## Why backtracking, concretely

`a+ab` against `aaab`: the `+` first swallows all three `a`s, then `ab` fails, so
it gives one back and retries. That "give one back and retry" *is* backtracking,
and it falls out naturally from recursion — a loop that tries `n`, `n-1`, `n-2`
occurrences and recurses on the rest for each.

Alternation is the same idea one level up: try the left branch, and if the
*remainder of the pattern* fails afterwards, try the right. This is why
alternation cannot be handled by matching branches in isolation — the failure
happens after the branch, and the branch must be retried.

## Backreferences and captures

`\1` means "the exact text the first group matched". So groups must record
their captured substrings as the match proceeds, and — crucially — **undo those
captures when the engine backtracks past them**. Captures that leak across
failed attempts produce matches that look almost right, which makes them the
hardest bug in the course to see.

Nested and multiple groups need numbering by opening-parenthesis order, which
is a parsing concern rather than a matching one.

## The stage arc

Literal → digits → alphanumerics → character groups → combining patterns →
anchors → quantifiers → wildcard → alternation → single backreference →
multiple backreferences → nested backreferences.

## Going deeper

- [Rob Pike's regex matcher, explained by Brian Kernighan](https://www.cs.princeton.edu/courses/archive/spr09/cos333/beautiful.html)
- [Russ Cox — Regular expression matching can be simple and fast](https://swtch.com/~rsc/regexp/regexp1.html)
- [`regexp/syntax` — how Go's engine models patterns](https://pkg.go.dev/regexp/syntax)
