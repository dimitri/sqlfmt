package format

import "strings"

// SQL/PGQ (ISO/IEC 9075-16), as PostgreSQL 19 implements it: property
// graphs declared over ordinary tables, queried with GRAPH_TABLE.
//
// The layout problem is the path pattern. PostgreSQL's grammar spells an
// edge out of single-character tokens -- "'-' '[' ... ']' '-' '>'" -- so
// the lexer hands us "-", "[", "]", "->" as separate tokens, and the
// generic expression path treats the "-" as a binary operator and breaks
// the line at it:
//
//	borders match(c is country) -[is borders]
//	-> (n is country) columns(n.name as neighbour)
//	^ was
//
// A quantifier fared worse, since "{1,4}" is four more tokens and the
// comma looked like a list separator:
//
//	-[is borders] -> { 1,
//	4 } (n is country)
//	^ was
//
// The saving grace is that a path pattern has no identifiers at paren
// depth zero: every vertex is "(...)", every full edge is "-[...]->", and
// the only bare tokens are the punctuation itself and a quantifier's
// digits. So "no spaces at depth zero" is the whole spacing rule, and the
// only place a line may break is between one element pattern and the next.

// graphPunct are the tokens a path pattern is built from. They never take
// a space on either side at pattern depth zero.
var graphPunct = map[string]bool{
	"-": true, "->": true, "<": true, ">": true,
	"[": true, "]": true, "{": true, "}": true,
}

// graphPatternJoin renders a MATCH pattern on one line. Inside a vertex or
// edge body -- "(c is country where c.name = 'France')" -- spacing is the
// usual one; at depth zero nothing is separated at all, except a comma
// between two path patterns, which is a list separator rather than part of
// a quantifier.
func graphPatternJoin(toks []Token) string {
	var b strings.Builder
	depth, inBrace := 0, false
	for i := range toks {
		t := toks[i]
		if i > 0 {
			prev := toks[i-1]
			switch {
			case depth > 0:
				// Inside an element body: ordinary spacing, except that
				// the body's own delimiters stay closed up. prevPrev has
				// to be real, not nil -- spaceBetween needs it to tell a
				// binary minus from a unary one, and without it
				// "where a.pop - 1 > 0" came out as "a.pop -1 > 0".
				var prevPrev *Token
				if i > 1 {
					prevPrev = &toks[i-2]
				}
				if prev.Text != "(" && prev.Text != "[" &&
					t.Text != ")" && t.Text != "]" &&
					spaceBetween(prevPrev, prev, t) {
					b.WriteString(" ")
				}
			case t.Text == "," || inBrace || t.Text == "{":
				// quantifier punctuation and its digits: never spaced
			case prev.Text == ",":
				b.WriteString(" ")
			}
		}
		b.WriteString(t.Text)
		switch t.Text {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		case "{":
			inBrace = true
		case "}":
			inBrace = false
		}
	}
	return b.String()
}

// graphTableJoin renders a whole GRAPH_TABLE on one line, using the
// pattern rules for the MATCH part and ordinary spacing either side. It is
// also the width estimate: measuring with plainJoin would count spaces
// around every "-" and "[" that the output will not contain.
func graphTableJoin(toks []Token) string {
	close := matchParen(toks, 1)
	name, pattern, columns, ok := splitGraphTable(toks[2:close])
	if !ok {
		return flatJoin(toks)
	}
	return "graph_table (" + flatJoin(name) +
		" match " + graphPatternJoin(pattern) +
		" columns " + flatJoin(columns) + ")" + flatJoin(toks[close+1:])
}

// graphEdgeStarts returns the indexes at pattern depth zero where an edge
// pattern begins -- the "-" or "<" that opens it. A wide pattern breaks
// only there, so a continuation line starts with the edge and carries the
// vertex it points at, rather than stranding a lone "(n is country)" under
// an arrow that ends the line above.
func graphEdgeStarts(toks []Token) []int {
	var out []int
	depth := 0
	for i := range toks {
		if depth == 0 && i > 0 {
			if toks[i].Text == "-" || toks[i].Text == "<" {
				out = append(out, i)
			}
		}
		switch toks[i].Text {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		}
	}
	return out
}

// isGraphTable reports whether toks[i] opens a "GRAPH_TABLE (" construct.
func isGraphTable(toks []Token, i int) bool {
	return toks[i].Kind == TokKeyword && toks[i].Lower == "graph_table" &&
		i+1 < len(toks) && toks[i+1].Text == "("
}

// splitGraphTable cuts a GRAPH_TABLE body -- everything between its outer
// parens -- into the graph name, the MATCH pattern, and the COLUMNS list
// (parens included). ok is false if it does not have that shape, in which
// case the caller falls back to its normal path rather than guessing.
func splitGraphTable(body []Token) (name, pattern, columns []Token, ok bool) {
	depth, mi, ci := 0, -1, -1
	for i := range body {
		switch body[i].Text {
		case "(", "[":
			depth++
			continue
		case ")", "]":
			depth--
			continue
		}
		if depth != 0 || body[i].Kind != TokKeyword {
			continue
		}
		if mi < 0 && body[i].Lower == "match" {
			mi = i
		} else if mi >= 0 && ci < 0 && body[i].Lower == "columns" {
			ci = i
		}
	}
	if mi < 0 || ci < 0 {
		return nil, nil, nil, false
	}
	return trimTokens(body[:mi]), trimTokens(body[mi+1 : ci]), trimTokens(body[ci+1:]), true
}

// layoutGraphTable renders a GRAPH_TABLE that does not fit on one line:
// the graph name stays with the opening paren, then MATCH and COLUMNS take
// a line each, indented under it. A pattern too wide even then breaks
// between element patterns, never inside one.
func layoutGraphTable(toks []Token, col int) []string {
	close := matchParen(toks, 1)
	name, pattern, columns, ok := splitGraphTable(toks[2:close])
	if !ok {
		return nil
	}
	inner := col + 2
	pad := strings.Repeat(" ", inner)

	lines := []string{"graph_table (" + flatJoin(name)}

	// "match " and "columns " are padded to a common width so the pattern
	// and the projection start in the same column, the way the clause
	// river works one level up.
	river := len("columns")
	kw := func(w string) string {
		return pad + strings.Repeat(" ", river-len(w)) + w + " "
	}

	patCol := inner + river + 1
	patLines := []string{graphPatternJoin(pattern)}
	if patCol+cols(patLines[0]) > targetWidth {
		patLines = breakGraphPattern(pattern, patCol)
	}
	lines = append(lines, kw("match")+patLines[0])
	for _, l := range patLines[1:] {
		lines = append(lines, strings.Repeat(" ", patCol)+l)
	}

	colTail := flatJoin(columns)
	lines = append(lines, kw("columns")+colTail+")"+flatJoin(toks[close+1:]))
	return lines
}

// breakGraphPattern splits a pattern across lines at element boundaries,
// packing as many elements onto each line as fit.
func breakGraphPattern(pattern []Token, col int) []string {
	starts := graphEdgeStarts(pattern)
	if len(starts) == 0 {
		return []string{graphPatternJoin(pattern)}
	}
	bounds := append([]int{0}, starts...)
	bounds = append(bounds, len(pattern))

	var out []string
	cur := ""
	for i := 0; i+1 < len(bounds); i++ {
		piece := graphPatternJoin(pattern[bounds[i]:bounds[i+1]])
		if cur == "" {
			cur = piece
			continue
		}
		if col+cols(cur)+cols(piece) <= targetWidth {
			cur += piece
			continue
		}
		out = append(out, cur)
		cur = piece
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// propGraphClauses are the continuation clauses of CREATE/ALTER PROPERTY
// GRAPH, which rule 22 puts one per line at indent 2 under the header.
var propGraphClauses = []clauseStarter{
	{"vertex", "tables"}, {"node", "tables"},
	{"edge", "tables"}, {"relationship", "tables"},
}

// propGraphElementClauses are the parts of one element table definition.
// A definition that does not fit on its own line hangs these under it, the
// way an over-wide ALTER TABLE subcommand hangs its inner clauses.
//
// "key" is deliberately absent: an edge definition carries three of them
// -- its own, SOURCE's and DESTINATION's -- and only the first is a clause
// of the element. Listing it would have split "source key (isocode)
// references country (isocode)" across two lines at the "key".
var propGraphElementClauses = []clauseStarter{
	{"source"}, {"destination"},
	{"label"}, {"default", "label"},
	{"properties"}, {"no", "properties"},
}

// layoutPropertyGraph handles CREATE/ALTER/DROP PROPERTY GRAPH, which
// otherwise fell through to flatJoin and came back as one 280-column line.
//
// The element table lists are the reason this needs more than
// layoutIndentedClauses: "vertex tables (...)" is a clause whose body is a
// comma list of definitions, each of which can itself be too wide.
func layoutPropertyGraph(toks []Token) []string {
	toks = trimTokens(toks)
	if !hasLineComment(toks) {
		if flat := flatJoin(toks); cols(flat) <= targetWidth-1 {
			return []string{flat}
		}
	}
	idx := clauseIndexes(toks, propGraphClauses)
	if len(idx) == 0 {
		return flatStatementLines(toks)
	}

	lines := renderRun(toks[:idx[0]], 0)
	for n, at := range idx {
		end := len(toks)
		if n+1 < len(idx) {
			end = idx[n+1]
		}
		lines = append(lines, layoutPropGraphClause(trimTokens(toks[at:end]))...)
	}
	return lines
}

// layoutPropGraphClause renders one "<vertex|edge> tables ( ... )" clause:
// the two keywords and the open paren, then one element table definition
// per line at indent 4, then the closing paren back at indent 2.
func layoutPropGraphClause(seg []Token) []string {
	if flat := flatJoin(seg); cols(flat)+2 <= targetWidth-1 {
		return []string{"  " + flat}
	}
	// "vertex tables" / "edge tables", then the parenthesized list.
	open := -1
	for i := range seg {
		if seg[i].Text == "(" {
			open = i
			break
		}
	}
	if open < 0 || matchParen(seg, open) != len(seg)-1 {
		sub := renderRun(seg, 2)
		out := []string{"  " + sub[0]}
		return append(out, sub[1:]...)
	}

	lines := []string{"  " + flatJoin(seg[:open]) + " ("}
	for i, it := range splitTopLevelComma(trimTokens(seg[open+1 : len(seg)-1])) {
		it = trimTokens(it)
		if len(it) == 0 {
			continue
		}
		if i > 0 {
			lines[len(lines)-1] += ","
		}
		item := renderRun(it, 4)
		if len(item) == 1 && 4+cols(item[0]) > targetWidth {
			if l, ok := layoutIndentedClauses(it, propGraphElementClauses, 6); ok && len(l) > 1 {
				item = l
			}
		}
		lines = append(lines, "    "+item[0])
		lines = append(lines, item[1:]...)
	}
	return append(lines, "  )")
}

// isPropertyGraphStmt reports whether toks is a CREATE/ALTER/DROP PROPERTY
// GRAPH statement, allowing for CREATE's optional TEMP.
func isPropertyGraphStmt(toks []Token) bool {
	for i := 1; i < len(toks) && i <= 3; i++ {
		if toks[i].Kind == TokKeyword && toks[i].Lower == "property" &&
			i+1 < len(toks) && toks[i+1].Lower == "graph" {
			return true
		}
	}
	return false
}
