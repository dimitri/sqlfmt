package format

import "strings"

// Expression layout, built on the Doc IR in doc.go.
//
// This covers the part of the language where break decisions interact:
// parenthesized groups, function-call argument lists, "||" concatenation
// chains and comparison operators. The clause layer above it -- the
// select/from/where river -- keeps its own layout, because its break
// points are fixed by the grammar rather than chosen.
//
// exprDoc is deliberately conservative about what it claims: it reports
// false for any run containing a construct the clause layer handles better
// (CASE, OVER, a subquery, an attached comment), and renderRun keeps its
// existing path for those. The point is to fix the expression cases, not
// to relitigate the ones that already work.

// exprDoc builds a Doc for a token run, or reports false when the run
// contains something this layer does not model.
func exprDoc(toks []Token) (Doc, bool) {
	toks = trimTokens(toks)
	if len(toks) == 0 {
		return Doc{}, false
	}
	for _, t := range toks {
		if isNonLayout(t) || len(t.Comments) > 0 || t.TrailingComment != nil {
			return Doc{}, false
		}
		// A subquery brings the whole clause layer with it, including its
		// own river; that stays where it is.
		if t.Kind == TokKeyword && (t.Lower == "select" || t.Lower == "with") {
			return Doc{}, false
		}
	}
	return exprConcatDoc(toks), true
}

// exprConcatDoc handles the outermost structure: a "||" chain, if there is
// one, else a comparison, else a plain run.
func exprConcatDoc(toks []Token) Doc {
	if parts := splitTopLevelConcat(toks); len(parts) > 1 {
		ds := make([]Doc, 0, len(parts)*2)
		for i, p := range parts {
			if i > 0 {
				ds = append(ds, line(), text("|| "))
			}
			ds = append(ds, exprCompareDoc(trimTokens(p)))
		}
		return group(concat(ds...))
	}
	return exprCompareDoc(toks)
}

// exprCompareDoc breaks at a top-level comparison operator between two
// parenthesized row constructors -- "(a, b) <> (x, y)" -- which is a break
// point no amount of breaking inside either list can substitute for.
func exprCompareDoc(toks []Token) Doc {
	if i := topLevelRowOp(toks); i > 0 {
		lhs, rhs := trimTokens(toks[:i]), trimTokens(toks[i+1:])
		if isParenGroup(lhs) && isParenGroup(rhs) {
			return group(concat(
				exprAtomDoc(lhs),
				line(),
				text(toks[i].Lower+" "),
				exprAtomDoc(rhs),
			))
		}
	}
	return exprAtomDoc(toks)
}

// exprAtomDoc renders a run that has no top-level operator structure left:
// a parenthesized group (possibly a call's argument list), or plain text.
func exprAtomDoc(toks []Token) Doc {
	toks = trimTokens(toks)
	if len(toks) == 0 {
		return text("")
	}
	// A CASE or a windowed aggregate is laid out by the clause layer, which
	// already does it well; the Doc layer only needs to know how wide it is
	// flat and how to ask for it at a given column.
	if d, ok := deferredConstruct(toks); ok {
		return d
	}
	// A select-list item usually carries an alias -- "format(...) as elem"
	// -- and the expression to lay out is what precedes it. Without peeling
	// it off the run does not end in ")", and the whole item fell through
	// to a single unbreakable Text.
	if expr, alias := splitTrailingAlias(toks); alias != "" {
		return concat(exprAtomDoc(expr), text(" "+alias))
	}
	// A trailing parenthesized group is the interesting case: a call's
	// arguments, a row constructor, an IN list.
	if toks[len(toks)-1].Text == ")" {
		if open := lastTopLevelOpen(toks); open >= 0 {
			head := plainJoin(toks[:open])
			inner := trimTokens(toks[open+1 : len(toks)-1])
			if len(inner) > 0 {
				items := splitTopLevelComma(inner)
				if len(items) > 1 {
					// Arguments fill rather than going one per line: a
					// five-argument call broken onto five lines is not
					// what the corpus does and reads worse than two.
					ds := make([]Doc, 0, len(items))
					for _, it := range items {
						ds = append(ds, exprConcatDoc(trimTokens(it)))
					}
					args := fill(concat(text(","), line()), ds...)
					_ = args
					// The arguments hang from the opening paren: soft()
					// after "(" so a broken group starts them on their own
					// line, nest() so they line up under it.
					return group(concat(
						text(head+"("),
						nest(2, concat(soft(), args)),
						soft(),
						text(")"),
					))
				}
			}
		}
	}
	return text(plainJoin(toks))
}

// deferredConstruct wraps a run that is exactly one CASE ... END, or one
// OVER(...) clause, as a docDefer.
func deferredConstruct(toks []Token) (Doc, bool) {
	if len(toks) == 0 || toks[0].Kind != TokKeyword {
		return Doc{}, false
	}
	switch toks[0].Lower {
	case "case":
		if end := matchCaseEnd(toks, 0); end == len(toks)-1 {
			run := toks
			return defer_(plainJoin(run), func(col int) []string {
				return layoutCase(run, col)
			}), true
		}
	case "over":
		if len(toks) > 1 && toks[1].Text == "(" && matchParen(toks, 1) == len(toks)-1 {
			run := toks
			return defer_(plainJoin(run), func(col int) []string {
				return layoutOver(run, col)
			}), true
		}
	}
	return Doc{}, false
}

// splitTrailingAlias peels a trailing column alias off an expression,
// returning the expression and the alias text ("as elem", "elem"), or an
// empty alias when there is none. Only an alias directly after a closing
// paren is recognised: that is the case this exists for, and it cannot be
// confused with an operator's right-hand side.
func splitTrailingAlias(toks []Token) ([]Token, string) {
	n := len(toks)
	if n >= 3 && toks[n-2].Kind == TokKeyword && toks[n-2].Lower == "as" &&
		toks[n-3].Text == ")" {
		return toks[:n-2], "as " + toks[n-1].Text
	}
	if n >= 2 && toks[n-1].Kind == TokIdent && toks[n-2].Text == ")" {
		return toks[:n-1], toks[n-1].Text
	}
	return toks, ""
}

// lastTopLevelOpen returns the index of the "(" matching the final ")", or
// -1 if the run is not one that ends in a balanced group.
func lastTopLevelOpen(toks []Token) int {
	depth := 0
	for i := len(toks) - 1; i >= 0; i-- {
		switch toks[i].Text {
		case ")":
			depth++
		case "(":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// exprLines renders a token run through the Doc layer, reporting false when
// the run is one this layer declines, or when the result is no better than
// the flat form the caller already has.
func exprLines(toks []Token, col int) ([]string, bool) {
	d, ok := exprDoc(toks)
	if !ok {
		return nil, false
	}
	if w := flatWidth(d); w >= 0 && col+w <= targetWidth {
		return nil, false // fits flat; nothing to decide
	}
	lines := renderDoc(d, col)
	if len(lines) < 2 {
		return nil, false
	}
	// Only worth it if it actually shortened the longest line.
	flat := plainJoin(trimTokens(toks))
	longest := 0
	for _, l := range lines {
		if n := len(l); n > longest {
			longest = n
		}
	}
	if longest >= col+len(flat) {
		return nil, false
	}
	return lines, true
}

var _ = strings.TrimSpace
