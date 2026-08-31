package format

import "strings"

// MERGE (SQL:2003, PostgreSQL 15+) layout.
//
// MERGE was not dispatched at all: "merge" was not a keyword, so
// statementKeyword returned "", and formatStatement's default arm ran
// flatJoin over the whole statement -- one 773-column line from a
// 76-column source, source subquery, join condition, WHEN clauses and all.
//
// The statement's shape is a head, a rivered USING/ON/AND group, and then
// one block per WHEN clause:
//
//	merge into constructor_season_summary as target
//	using (
//	    select ...
//	) as source
//	   on target.season = source.season
//	  and target.constructorid = source.constructorid
//	when matched then
//	  update set points = source.points
//	when not matched then
//	  insert (season, constructorid)
//	  values (source.season, source.constructorid)
//
// The river covers USING/ON/AND only, at width 5. The head carries the
// target table and the WHEN clauses are their own blocks, for the same
// reason layoutDDL keeps its "create ..." line out of its river: they are
// long enough that including them would push everything else off the page.

// mergeRiver is the width of the USING/ON/AND river: len("using").
const mergeRiver = 5

// layoutMerge renders a MERGE statement.
func layoutMerge(toks []Token) []string {
	usingIdx := topLevelKeyword(toks, 0, "using")
	if usingIdx < 0 {
		return []string{flatJoin(toks)}
	}
	onIdx := topLevelKeyword(toks, usingIdx+1, "on")
	if onIdx < 0 {
		return []string{flatJoin(toks)}
	}
	whenIdx := topLevelKeyword(toks, onIdx+1, "when")
	if whenIdx < 0 {
		whenIdx = len(toks)
	}

	lines := []string{flatJoin(toks[:usingIdx])}

	// USING <source>. A parenthesized subquery is laid out as a query, with
	// any alias that follows the closing paren kept on its line.
	lines = append(lines, mergeUsingLines(trimTokens(toks[usingIdx+1:onIdx]))...)

	// ON <cond> [AND <cond>]...
	preds, ops := splitAndOr(trimTokens(toks[onIdx+1 : whenIdx]))
	for i, pred := range preds {
		kw := "on"
		if i > 0 {
			kw = ops[i-1]
		}
		pad := mergeRiver - len(kw)
		if pad < 0 {
			pad = 0
		}
		pl := renderRun(trimTokens(pred), mergeRiver+1)
		lines = append(lines, strings.Repeat(" ", pad)+kw+" "+pl[0])
		lines = append(lines, pl[1:]...)
	}

	// One block per WHEN clause.
	for _, seg := range splitMergeWhens(toks[whenIdx:]) {
		lines = append(lines, mergeWhenLines(seg)...)
	}
	return lines
}

// mergeUsingLines renders the USING clause's source relation.
func mergeUsingLines(src []Token) []string {
	if len(src) > 0 && src[0].Text == "(" {
		if close := matchParen(src, 0); close > 0 {
			inner := trimTokens(src[1:close])
			if isQueryStart(inner) {
				out := []string{"using ("}
				for _, l := range formatQuerySegment(inner, 4) {
					out = append(out, l)
				}
				tail := ")"
				if rest := trimTokens(src[close+1:]); len(rest) > 0 {
					tail += " " + flatJoin(rest)
				}
				return append(out, tail)
			}
		}
	}
	return []string{"using " + flatJoin(src)}
}

// splitMergeWhens splits the tail of a MERGE into its WHEN clauses.
func splitMergeWhens(toks []Token) [][]Token {
	var segs [][]Token
	start := -1
	for i := 0; i < len(toks); i++ {
		if isTopLevelKeywordAt(toks, i, "when") {
			if start >= 0 {
				segs = append(segs, toks[start:i])
			}
			start = i
		}
	}
	if start >= 0 {
		segs = append(segs, toks[start:])
	}
	return segs
}

// mergeWhenLines renders one "when ... then <action>" clause: the condition
// on its own line, the action indented under it.
func mergeWhenLines(seg []Token) []string {
	thenIdx := topLevelKeyword(seg, 0, "then")
	if thenIdx < 0 {
		return []string{flatJoin(seg)}
	}
	head := flatJoin(seg[:thenIdx]) + " then"
	action := trimTokens(seg[thenIdx+1:])
	if len(action) == 0 {
		return []string{head}
	}
	out := []string{head}
	for _, l := range formatStatementLines(action) {
		out = append(out, "  "+l)
	}
	return out
}

// formatStatementLines formats a MERGE action -- an UPDATE SET, an INSERT
// with its VALUES, a DELETE, a DO NOTHING -- as the statement fragment it
// is. Each is a clause sequence, so formatQuerySegment handles them; a bare
// DELETE or DO NOTHING has no clauses and stays flat.
func formatStatementLines(toks []Token) []string {
	lines := formatQuerySegment(toks, 0)
	if len(lines) == 0 {
		return []string{flatJoin(toks)}
	}
	// splitClauses drops anything before its first clause bound, so a
	// fragment whose leading tokens are not a clause keyword would lose
	// them silently. Fall back to the flat form rather than emit a
	// statement with a piece missing.
	if squashTokens(toks) != squashLines(lines) {
		return []string{flatJoin(toks)}
	}
	return lines
}

// squashTokens / squashLines reduce a fragment to a comparable form:
// no whitespace, lowercased.
func squashTokens(toks []Token) string {
	var b strings.Builder
	for _, t := range toks {
		if isNonLayout(t) {
			continue
		}
		b.WriteString(strings.ToLower(t.Text))
	}
	return strings.Join(strings.Fields(b.String()), "")
}

func squashLines(lines []string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.Join(lines, " "))), "")
}

// topLevelKeyword returns the index of the first top-level keyword kw at or
// after from, or -1.
func topLevelKeyword(toks []Token, from int, kw string) int {
	for i := from; i < len(toks); i++ {
		if isTopLevelKeywordAt(toks, i, kw) {
			return i
		}
	}
	return -1
}

// isTopLevelKeywordAt reports whether toks[i] is kw at paren depth 0.
func isTopLevelKeywordAt(toks []Token, i int, kw string) bool {
	if toks[i].Lower != kw {
		return false
	}
	depth := 0
	for j := 0; j < i; j++ {
		switch toks[j].Text {
		case "(":
			depth++
		case ")":
			depth--
		}
	}
	return depth == 0
}
