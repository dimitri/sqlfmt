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
	return exprDocLevels(toks, binaryLevels)
}

// exprDocLevels is exprDoc with an explicit operator set; see
// predicateLevels for why the caller chooses it.
func exprDocLevels(toks []Token, levels [][]string) (Doc, bool) {
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
	return exprConcatDoc(toks, levels), true
}

// exprConcatDoc handles the outermost structure: a "||" chain, if there is
// one, else a comparison, else a plain run.
func exprConcatDoc(toks []Token, levels [][]string) Doc {
	if parts, ops := splitTopLevelBinary(toks, levels); len(parts) > 1 {
		ds := make([]Doc, 0, len(parts)*2)
		ds = append(ds, exprCompareDoc(trimTokens(parts[0]), levels))
		for i, p := range parts[1:] {
			// The operator hangs into the gutter left of the operands and
			// every operand starts at the same column, the way the clause
			// river sets "and"/"or" under "where". Putting the operator at
			// the operand column instead pushes each continuation operand
			// right by the operator's width, so a chain no longer lines up
			// with the operand it started from.
			op := ops[i]
			ds = append(ds,
				nest(-(cols(op)+1), concat(line(), text(op+" "))),
				exprCompareDoc(trimTokens(p), levels))
		}
		return group(concat(ds...))
	}
	return exprCompareDoc(toks, levels)
}

// exprCompareDoc breaks at a top-level comparison operator between two
// parenthesized row constructors -- "(a, b) <> (x, y)" -- which is a break
// point no amount of breaking inside either list can substitute for.
func exprCompareDoc(toks []Token, levels [][]string) Doc {
	if i := topLevelRowOp(toks); i > 0 {
		lhs, rhs := trimTokens(toks[:i]), trimTokens(toks[i+1:])
		if isParenGroup(lhs) && isParenGroup(rhs) {
			return group(concat(
				exprAtomDoc(lhs, levels),
				line(),
				text(toks[i].Lower+" "),
				exprAtomDoc(rhs, levels),
			))
		}
	}
	return exprAtomDoc(toks, levels)
}

// exprAtomDoc renders a run that has no top-level operator structure left:
// a parenthesized group (possibly a call's argument list), or plain text.
func exprAtomDoc(toks []Token, levels [][]string) Doc {
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
		return concat(exprAtomDoc(expr, levels), text(" "+alias))
	}
	// Same problem as the alias: "extract(...)::int" ends in a type name,
	// not in ")", so the paren logic below never saw the call it wraps.
	if expr, cast := splitTrailingCast(toks); cast != "" {
		return concat(exprAtomDoc(expr, levels), text(cast))
	}
	// "avg(x) over(...)" and "avg(x) over(...)::numeric" once the cast is
	// peeled: the OVER clause has a layout of its own, but the run does
	// not start with it, so deferredConstruct never fired -- and the paren
	// logic below is right to refuse to hang a clause paren, which left
	// the whole thing as one unbreakable atom. Defer the OVER in place
	// instead, so it can lay itself out at whatever column it lands at.
	if head, over, ok := splitTrailingOver(toks); ok {
		return concat(
			text(plainJoin(head)+" "),
			defer_(plainJoin(over), func(col int) []string {
				return layoutOver(over, col)
			}),
		)
	}
	// A trailing parenthesized group is the interesting case: a call's
	// arguments, a row constructor, an IN list.
	if toks[len(toks)-1].Text == ")" {
		if open := lastTopLevelOpen(toks); open >= 0 {
			head := plainJoin(toks[:open])
			inner := trimTokens(toks[open+1 : len(toks)-1])
			if len(inner) > 0 {
				items := splitTopLevelComma(inner)
				// A lone argument may hang from its paren too, so a
				// single wide call has somewhere to break -- but not when
				// the paren opens a clause rather than an argument list.
				// OVER(...), FILTER(...) and WITHIN GROUP(...) each read
				// as one unit with a good one-line form, and hanging them
				// turned a fitting window frame into three lines: shorter
				// and worse.
				// It must also actually be a call: "(a + b)::text" is a
				// grouping paren with no callee in front of it, and
				// hanging its one item strands the "(" on a line of its
				// own above a stray ")".
				soleOK := len(items) == 1 && isCallParen(toks, open) &&
					!clauseParen(toks, open)
				if len(items) > 1 || soleOK {
					// Arguments fill rather than going one per line: a
					// five-argument call broken onto five lines is not
					// what the corpus does and reads worse than two.
					ds := make([]Doc, 0, len(items))
					for _, it := range items {
						ds = append(ds, exprConcatDoc(trimTokens(it), levels))
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

// isCallParen reports whether the "(" at i follows a name, i.e. opens a
// call's argument list rather than grouping an expression.
func isCallParen(toks []Token, i int) bool {
	return i > 0 && (toks[i-1].Kind == TokIdent || toks[i-1].Kind == TokKeyword)
}

// clauseParen reports whether the "(" at i opens a clause -- OVER,
// FILTER, WITHIN GROUP -- rather than a call's argument list.
func clauseParen(toks []Token, i int) bool {
	if i == 0 || toks[i-1].Kind != TokKeyword {
		return false
	}
	switch toks[i-1].Lower {
	case "over", "filter":
		return true
	case "group":
		return i > 1 && toks[i-2].Kind == TokKeyword && toks[i-2].Lower == "within"
	}
	return false
}

// splitTrailingOver cuts a run that ends in an OVER(...) clause into the
// windowed expression and the clause itself. The run must not be the OVER
// alone -- deferredConstruct already handles that.
func splitTrailingOver(toks []Token) ([]Token, []Token, bool) {
	depth := 0
	for i := range toks {
		switch toks[i].Text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth != 0 || i == 0 || toks[i].Kind != TokKeyword || toks[i].Lower != "over" {
			continue
		}
		if i+1 < len(toks) && toks[i+1].Text == "(" && matchParen(toks, i+1) == len(toks)-1 {
			return toks[:i], toks[i:], true
		}
	}
	return nil, nil, false
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

// splitTrailingCast peels a trailing "::type" (including a schema
// qualification and array brackets) off an expression, so what remains
// ends in the ")" the paren layout keys on.
func splitTrailingCast(toks []Token) ([]Token, string) {
	n := len(toks)
	// Walk back over "[]" pairs and a dotted type name to the "::".
	i := n
	for i > 0 && toks[i-1].Text == "]" {
		if i < 2 || toks[i-2].Text != "[" {
			return toks, ""
		}
		i -= 2
	}
	for i > 0 && (toks[i-1].Kind == TokIdent || toks[i-1].Kind == TokKeyword) {
		i--
		if i > 0 && toks[i-1].Text == "." {
			i--
			continue
		}
		break
	}
	if i < 2 || toks[i-1].Text != "::" || i-1 == 0 {
		return toks, ""
	}
	return toks[:i-1], plainJoin(toks[i-1:])
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
	return exprLinesLevels(toks, col, binaryLevels)
}

// predicateLines lays out a condition -- a WHERE body, a JOIN ... ON --
// which may also break at a comparison operator.
func predicateLines(toks []Token, col int) ([]string, bool) {
	return exprLinesLevels(toks, col, predicateLevels)
}

func exprLinesLevels(toks []Token, col int, levels [][]string) ([]string, bool) {
	d, ok := exprDocLevels(toks, levels)
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
		if n := cols(l); n > longest {
			longest = n
		}
	}
	if longest >= col+cols(flat) {
		return nil, false
	}
	return lines, true
}

var _ = strings.TrimSpace
