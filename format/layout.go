package format

import "strings"

const targetWidth = 78

// clauseWords lists the recognized clause-starting keyword sequences, in the
// order they're checked. Two-word entries must be checked before any
// single-word entry that shares their first word.
var clauseWords = []struct {
	words []string
	name  string
}{
	{[]string{"insert", "into"}, "insert into"},
	{[]string{"on", "conflict"}, "on conflict"},
	{[]string{"group", "by"}, "group by"},
	{[]string{"order", "by"}, "order by"},
	{[]string{"select"}, "select"},
	{[]string{"from"}, "from"},
	{[]string{"where"}, "where"},
	{[]string{"having"}, "having"},
	{[]string{"update"}, "update"},
	{[]string{"set"}, "set"},
	{[]string{"delete"}, "delete"},
	{[]string{"returning"}, "returning"},
	{[]string{"values"}, "values"},
	{[]string{"limit"}, "limit"},
	{[]string{"offset"}, "offset"},
}

type clauseSeg struct {
	name string
	body []Token
}

// isNonLayout reports whether a token carries no layout weight of its own
// (comments are attached to real tokens and never drive clause boundaries).
func isNonLayout(t Token) bool {
	return t.Kind == TokLineComment || t.Kind == TokBlockComment
}

// matchParen returns the index of the ")" matching the "(" at openIdx.
func matchParen(toks []Token, openIdx int) int {
	depth := 0
	for i := openIdx; i < len(toks); i++ {
		switch toks[i].Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(toks) - 1
}

// matchCaseEnd returns the index of the "end" matching the "case" at caseIdx,
// accounting for nested CASE expressions.
func matchCaseEnd(toks []Token, caseIdx int) int {
	depth := 0
	for i := caseIdx; i < len(toks); i++ {
		if toks[i].Kind != TokKeyword {
			continue
		}
		switch toks[i].Lower {
		case "case":
			depth++
		case "end":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(toks) - 1
}

// splitTopLevel splits toks on every top-level (paren-depth 0) token whose
// Lower matches sep, returning the segments between separators.
func splitTopLevel(toks []Token, sep string) [][]Token {
	var segs [][]Token
	depth := 0
	start := 0
	for i, t := range toks {
		if t.Text == "(" {
			depth++
		} else if t.Text == ")" {
			depth--
		} else if depth == 0 && t.Kind == TokKeyword && t.Lower == sep {
			segs = append(segs, toks[start:i])
			start = i + 1
		}
	}
	segs = append(segs, toks[start:])
	return segs
}

// splitTopLevelComma splits a top-level comma-separated list.
func splitTopLevelComma(toks []Token) [][]Token {
	var segs [][]Token
	depth := 0
	start := 0
	for i, t := range toks {
		if t.Text == "(" {
			depth++
		} else if t.Text == ")" {
			depth--
		} else if depth == 0 && t.Text == "," {
			segs = append(segs, toks[start:i])
			start = i + 1
		}
	}
	segs = append(segs, toks[start:])
	return segs
}

// splitClauses cuts a query's top-level tokens into clauses at recognized
// clause-keyword boundaries (STYLE.md's clause list, plus limit/offset which
// participate in the same river alignment in real corpus output).
func splitClauses(toks []Token) []clauseSeg {
	var segs []clauseSeg
	depth := 0
	type bound struct {
		idx   int
		name  string
		kwEnd int
	}
	var bounds []bound
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Text == "(" {
			depth++
			continue
		}
		if t.Text == ")" {
			depth--
			continue
		}
		if depth != 0 || t.Kind != TokKeyword {
			continue
		}
		for _, cw := range clauseWords {
			if matchWords(toks, i, cw.words) {
				bounds = append(bounds, bound{idx: i, name: cw.name, kwEnd: i + len(cw.words)})
				break
			}
		}
	}
	if len(bounds) == 0 {
		return nil
	}
	for bi, b := range bounds {
		end := len(toks)
		if bi+1 < len(bounds) {
			end = bounds[bi+1].idx
		}
		segs = append(segs, clauseSeg{name: b.name, body: toks[b.kwEnd:end]})
	}
	return segs
}

func matchWords(toks []Token, i int, words []string) bool {
	for j, w := range words {
		if i+j >= len(toks) || toks[i+j].Kind != TokKeyword || toks[i+j].Lower != w {
			return false
		}
	}
	return true
}

func isJoinModifier(lower string) bool {
	switch lower {
	case "left", "right", "full", "inner", "outer", "cross":
		return true
	}
	return false
}

// splitJoinSegments splits a FROM clause's tokens into the leading table
// expression and each subsequent JOIN segment (starting at its modifier, if
// any, else at "join" itself).
func splitJoinSegments(toks []Token) [][]Token {
	var segs [][]Token
	depth := 0
	start := 0
	for i, t := range toks {
		if t.Text == "(" {
			depth++
			continue
		}
		if t.Text == ")" {
			depth--
			continue
		}
		if depth != 0 || t.Kind != TokKeyword {
			continue
		}
		if isJoinModifier(t.Lower) {
			segs = append(segs, toks[start:i])
			start = i
			continue
		}
		if t.Lower == "join" {
			prevIsModifier := i > 0 && toks[i-1].Kind == TokKeyword && isJoinModifier(toks[i-1].Lower)
			if !prevIsModifier {
				segs = append(segs, toks[start:i])
				start = i
			}
		}
	}
	segs = append(segs, toks[start:])
	return segs
}

// joinKeywordEnd returns the index just past a join segment's leading
// keyword phrase (left/right/.../join).
func joinKeywordEnd(seg []Token) int {
	kEnd := 0
	for kEnd < len(seg) && seg[kEnd].Kind == TokKeyword {
		kEnd++
	}
	return kEnd
}

// maxJoinPhraseLen scans a FROM clause's tokens for the longest join-keyword
// phrase ("join", "left join", ...) actually present.
func maxJoinPhraseLen(fromBody []Token) int {
	max := 0
	segs := splitJoinSegments(fromBody)
	for _, seg := range segs[1:] {
		seg = trimTokens(seg)
		if len(seg) == 0 {
			continue
		}
		if n := len(flatJoin(seg[:joinKeywordEnd(seg)])); n > max {
			max = n
		}
	}
	return max
}

// riverWidth computes the STYLE.md "unifying rule" column width for a query
// level: the longest clause keyword actually present, or -- when a FROM
// clause contains a JOIN longer than any clause keyword -- the longest JOIN
// phrase (plus headroom) instead, so the whole clause list (select/from/
// where/...) pads consistently with where JOIN's own table names end up.
// An unindented "left join" would otherwise read as a mistake, not as
// intentional flush-left alignment the way a bare wide clause keyword does.
func riverWidth(segs []clauseSeg) int {
	w := 0
	for _, s := range segs {
		if len(s.name) > w {
			w = len(s.name)
		}
		if s.name == "from" {
			if j := maxJoinPhraseLen(s.body) + 2; j > w {
				w = j
			}
		}
	}
	return w
}

// renderTokenText renders a single token's own text (keywords lowercased).
func renderTokenText(t Token) string {
	if t.Kind == TokKeyword {
		return t.Lower
	}
	return t.Text
}

// spaceBetween decides whether a space belongs between two adjacent
// rendered tokens (STYLE.md rules 4 and 5: no space around "." or "::", no
// space before "," "(" ")" ";" "]", no space after "(" "[").
func spaceBetween(prev, next Token) bool {
	if prev.Text == "(" || prev.Text == "[" {
		return false
	}
	if next.Text == ")" || next.Text == "]" || next.Text == "," || next.Text == ";" {
		return false
	}
	if prev.Text == "." || next.Text == "." {
		return false
	}
	if prev.Text == "::" || next.Text == "::" {
		return false
	}
	if next.Text == "(" {
		// function-call / using(/over( style: no space before "(". A few
		// keywords double as common function names (left(...), right(...)
		// for string truncation) even though they're also JOIN modifiers;
		// treat them as calls here too since "(" never directly follows
		// them in JOIN-modifier position (join modifiers are always
		// followed by "join", never "(").
		if prev.Kind == TokIdent || prev.Lower == "using" || prev.Lower == "over" ||
			prev.Lower == "left" || prev.Lower == "right" {
			return false
		}
		return true
	}
	return true
}

// renderRun renders a flat token run (no clause keywords) starting at
// column col, recursing into CASE expressions, OVER(...) clauses,
// parenthesized subqueries and plain parenthesized groups. It returns
// rendered lines; line 0 has no leading indent (the caller has already
// written up to col), later lines carry full absolute indentation.
func renderRun(toks []Token, col int) []string {
	lines := []string{""}
	curCol := col
	write := func(s string) {
		lines[len(lines)-1] += s
		if nl := strings.LastIndexByte(s, '\n'); nl >= 0 {
			curCol = len(s) - nl - 1
		} else {
			curCol += len(s)
		}
	}
	merge := func(more []string) {
		if len(more) == 0 {
			return
		}
		write(more[0])
		lines = append(lines, more[1:]...)
		if len(more) > 1 {
			curCol = len(lines[len(lines)-1])
		}
	}

	var prev *Token
	i := 0
	for i < len(toks) {
		t := toks[i]
		if isNonLayout(t) {
			if prev != nil {
				write(" ")
			}
			write(t.Text)
			prev = &toks[i]
			i++
			continue
		}

		if t.Kind == TokKeyword && t.Lower == "case" {
			end := matchCaseEnd(toks, i)
			if prev != nil && spaceBetween(*prev, t) {
				write(" ")
			}
			merge(layoutCase(toks[i:end+1], curCol))
			prev = &toks[end]
			i = end + 1
			continue
		}

		if t.Kind == TokKeyword && t.Lower == "over" && i+1 < len(toks) && toks[i+1].Text == "(" {
			close := matchParen(toks, i+1)
			overToks := toks[i : close+1]
			if prev != nil && spaceBetween(*prev, t) {
				write(" ")
			}
			flat := plainJoin(overToks)
			if curCol+len(flat) <= targetWidth {
				write(flat)
			} else {
				merge(layoutOver(overToks, curCol))
			}
			prev = &toks[close]
			i = close + 1
			continue
		}

		if t.Text == "(" {
			close := matchParen(toks, i)
			inner := toks[i+1 : close]
			needSpace := prev != nil && spaceBetween(*prev, t)
			isSubquery := len(inner) > 0 && inner[0].Kind == TokKeyword && (inner[0].Lower == "select" || inner[0].Lower == "with")
			if needSpace {
				write(" ")
			}
			if isSubquery {
				content := formatQuerySegment(inner, curCol)
				if len(content) > 1 {
					// A multi-line subquery deep inside an expression would
					// otherwise hang off whatever column its enclosing
					// paren happened to reach; break it onto its own,
					// boundedly-indented lines instead (STYLE.md rule 12,
					// explicitly best-effort).
					breakIndent := leadingSpaces(lines[len(lines)-1]) + 2
					content = formatQuerySegment(inner, breakIndent)
					write("(")
					lines = append(lines, strings.Repeat(" ", breakIndent))
					curCol = breakIndent
					merge(content)
					closeIndent := breakIndent - 2
					if closeIndent < 0 {
						closeIndent = 0
					}
					lines = append(lines, strings.Repeat(" ", closeIndent)+")")
					curCol = closeIndent + 1
				} else {
					// Content fits on one line: keep the closing paren
					// glued to it too, rather than forcing an extra line.
					write("(")
					merge(content)
					write(")")
				}
			} else {
				content := renderRun(inner, curCol)
				if len(content) > 1 {
					breakIndent := leadingSpaces(lines[len(lines)-1]) + 2
					content = renderRun(inner, breakIndent)
					write("(")
					lines = append(lines, strings.Repeat(" ", breakIndent))
					curCol = breakIndent
					merge(content)
					write(")")
				} else {
					write("(")
					merge(content)
					write(")")
				}
			}
			prev = &toks[close]
			i = close + 1
			continue
		}

		if prev != nil && spaceBetween(*prev, t) {
			write(" ")
		}
		write(renderTokenText(t))
		prev = &toks[i]
		i++
	}
	return lines
}

// flatJoin renders a token run as a single line, ignoring width limits;
// used to test whether a construct fits inline before deciding to wrap it.
func flatJoin(toks []Token) string {
	lines := renderRun(toks, 0)
	return strings.Join(lines, " ")
}

// plainJoin renders a token run as a single flat line without dispatching
// to CASE/OVER/subquery special-casing. It exists for width-estimate checks
// on constructs (a CASE...END span, an OVER(...) span) that already start
// with the very keyword renderRun would otherwise recurse back into via
// flatJoin, which would recreate the construct it's trying to measure.
func plainJoin(toks []Token) string {
	var sb strings.Builder
	var prev *Token
	for i := range toks {
		t := toks[i]
		if isNonLayout(t) {
			continue
		}
		if prev != nil && spaceBetween(*prev, t) {
			sb.WriteByte(' ')
		}
		sb.WriteString(renderTokenText(t))
		prev = &toks[i]
	}
	return sb.String()
}

// layoutCase renders a CASE...END expression. caseCol is the column the
// "case" keyword itself starts at; WHEN stays near it, THEN/ELSE align
// under WHEN's condition-start column, END dedents back near CASE's column.
// Alignment is always recomputed fresh per call (STYLE.md rule 15).
func layoutCase(toks []Token, caseCol int) []string {
	flat := plainJoin(toks)
	if caseCol+len(flat) <= targetWidth {
		return []string{flat}
	}

	// toks[0] == "case". Split remaining into when/then/else branches.
	whenCol := caseCol + len("case ")
	lines := []string{"case"}
	i := 1
	for i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "when" {
		condStart := i + 1
		thenIdx := -1
		depth := 0
		for j := condStart; j < len(toks); j++ {
			if toks[j].Text == "(" {
				depth++
			} else if toks[j].Text == ")" {
				depth--
			} else if depth == 0 && toks[j].Kind == TokKeyword && toks[j].Lower == "then" {
				thenIdx = j
				break
			}
		}
		if thenIdx == -1 {
			break
		}
		elseOrWhenOrEnd := len(toks)
		depth = 0
		for j := thenIdx + 1; j < len(toks); j++ {
			if toks[j].Text == "(" {
				depth++
			} else if toks[j].Text == ")" {
				depth--
			} else if depth == 0 && toks[j].Kind == TokKeyword && (toks[j].Lower == "when" || toks[j].Lower == "else" || toks[j].Lower == "end") {
				elseOrWhenOrEnd = j
				break
			}
		}
		condLines := renderRun(toks[condStart:thenIdx], whenCol+len("when "))
		lines = append(lines, strings.Repeat(" ", whenCol)+"when "+condLines[0])
		lines = append(lines, condLines[1:]...)
		thenLines := renderRun(toks[thenIdx+1:elseOrWhenOrEnd], whenCol+len("then "))
		lines = append(lines, strings.Repeat(" ", whenCol)+"then "+thenLines[0])
		lines = append(lines, thenLines[1:]...)
		i = elseOrWhenOrEnd
	}
	if i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "else" {
		endIdx := len(toks)
		depth := 0
		for j := i + 1; j < len(toks); j++ {
			if toks[j].Text == "(" {
				depth++
			} else if toks[j].Text == ")" {
				depth--
			} else if depth == 0 && toks[j].Kind == TokKeyword && toks[j].Lower == "end" {
				endIdx = j
				break
			}
		}
		elseLines := renderRun(toks[i+1:endIdx], whenCol+len("else "))
		lines = append(lines, strings.Repeat(" ", whenCol)+"else "+elseLines[0])
		lines = append(lines, elseLines[1:]...)
		i = endIdx
	}
	lines = append(lines, strings.Repeat(" ", caseCol)+"end")
	return lines
}

// layoutOver wraps a long OVER(...) clause: PARTITION BY / ORDER BY / frame
// clause each on their own line under the opening paren's column.
// splitTopLevelTwoWord splits toks at the first top-level occurrence of the
// two-word keyword "w1 w2" (e.g. "partition by", "order by"), consuming
// both words, returning the tokens before and after.
func splitTopLevelTwoWord(toks []Token, w1, w2 string) [][]Token {
	depth := 0
	for i, t := range toks {
		if t.Text == "(" {
			depth++
		} else if t.Text == ")" {
			depth--
		} else if depth == 0 && t.Kind == TokKeyword && t.Lower == w1 &&
			i+1 < len(toks) && toks[i+1].Kind == TokKeyword && toks[i+1].Lower == w2 {
			return [][]Token{toks[:i], toks[i+2:]}
		}
	}
	return [][]Token{toks}
}

func layoutOver(toks []Token, overCol int) []string {
	openCol := overCol + len("over(")
	// toks: over ( ... )
	inner := toks[2 : len(toks)-1]
	segs := splitTopLevelTwoWord(inner, "partition", "by")
	var lines []string
	first := true
	emit := func(prefix string, body []Token) {
		bodyLines := renderRun(body, openCol+len(prefix))
		text := strings.Repeat(" ", openCol) + prefix + strings.TrimSpace(bodyLines[0])
		if first {
			lines = append(lines, "over("+strings.TrimPrefix(text, strings.Repeat(" ", openCol)))
			first = false
		} else {
			lines = append(lines, text)
		}
		lines = append(lines, bodyLines[1:]...)
	}
	if len(segs) == 2 {
		emit("partition by ", segs[1])
	} else {
		lines = append(lines, "over(")
		first = false
	}
	orderSegs := splitTopLevelTwoWord(segs[0], "order", "by")
	if len(orderSegs) == 2 {
		emit("order by ", orderSegs[1])
	}
	lines = append(lines, strings.Repeat(" ", overCol)+")")
	return lines
}

// layoutCommaList renders a comma-separated list. If the flat form fits
// within targetWidth it stays on one line; otherwise each item goes on its
// own line, aligned under startCol, with the comma at the end of the
// previous line.
func layoutCommaList(toks []Token, startCol int) []string {
	items := splitTopLevelComma(trimTokens(toks))
	flat := flatJoin(trimTokens(toks))
	if startCol+len(flat) <= targetWidth && len(items) > 0 {
		return []string{flat}
	}
	var lines []string
	for idx, it := range items {
		it = trimTokens(it)
		itLines := renderRun(it, startCol)
		if idx < len(items)-1 {
			// The separating comma belongs on the item's own last rendered
			// line, not necessarily its first -- an item that itself wraps
			// (e.g. a long CASE expression) has already produced several
			// lines by this point.
			itLines[len(itLines)-1] += ","
		}
		if idx == 0 {
			lines = append(lines, itLines[0])
		} else {
			lines = append(lines, strings.Repeat(" ", startCol)+itLines[0])
		}
		lines = append(lines, itLines[1:]...)
	}
	return lines
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func trimTokens(toks []Token) []Token {
	i, j := 0, len(toks)
	for i < j && isNonLayout(toks[i]) {
		i++
	}
	for j > i && isNonLayout(toks[j-1]) {
		j--
	}
	return toks[i:j]
}

// layoutPredicateList renders a WHERE/ON-style AND/OR-joined predicate list,
// one predicate per line, with AND/OR at the start of continuation lines,
// right-aligned so the keyword ends at endCol.
func layoutPredicateList(toks []Token, endCol int) []string {
	toks = trimTokens(toks)
	preds, ops := splitAndOr(toks)
	var lines []string
	startCol := endCol + 1
	for idx, p := range preds {
		p = trimTokens(p)
		// idx 0's line is appended directly after the clause keyword by the
		// caller (renderClause), so it carries no prefix of its own here;
		// only continuation lines get a right-aligned AND/OR prefix.
		if idx == 0 {
			pLines := renderRun(p, startCol)
			lines = append(lines, pLines...)
			continue
		}
		kw := ops[idx-1]
		pad := endCol - len(kw)
		if pad < 0 {
			pad = 0
		}
		pLines := renderRun(p, startCol)
		prefix := strings.Repeat(" ", pad) + kw + " "
		lines = append(lines, prefix+pLines[0])
		lines = append(lines, pLines[1:]...)
	}
	return lines
}

// splitAndOr splits a top-level predicate list on top-level AND/OR keywords,
// returning the predicates and, for each boundary, the connecting keyword.
func splitAndOr(toks []Token) ([][]Token, []string) {
	var preds [][]Token
	var ops []string
	depth := 0
	start := 0
	for i, t := range toks {
		if t.Text == "(" {
			depth++
		} else if t.Text == ")" {
			depth--
		} else if depth == 0 && t.Kind == TokKeyword && (t.Lower == "and" || t.Lower == "or") {
			preds = append(preds, toks[start:i])
			ops = append(ops, t.Lower)
			start = i + 1
		}
	}
	preds = append(preds, toks[start:])
	return preds, ops
}

// formatQuerySegment formats a SELECT/INSERT/UPDATE/DELETE (optionally
// WITH-prefixed) query body at the given base indent, returning its
// rendered lines. It is the single recursive entry point reused for
// top-level statements, CTE bodies, and subqueries.
func formatQuerySegment(toks []Token, baseIndent int) []string {
	toks = trimTokens(toks)
	if len(toks) > 0 && toks[0].Kind == TokKeyword && toks[0].Lower == "with" {
		ctes, rest := parseCTEs(toks)
		var lines []string
		for idx, c := range ctes {
			cteLines := renderCTE(c, baseIndent, idx < len(ctes)-1)
			if idx == 0 {
				cteLines[0] = strings.Repeat(" ", baseIndent) + "with " + strings.TrimSpace(cteLines[0])
			}
			lines = append(lines, cteLines...)
		}
		lines = append(lines, formatQuerySegment(rest, baseIndent)...)
		return lines
	}

	segs := splitClauses(toks)
	if segs == nil {
		return []string{flatJoin(toks)}
	}
	width := riverWidth(segs)

	if fitsInline(segs, baseIndent, width) {
		return []string{flatJoin(toks)}
	}

	var lines []string
	for _, s := range segs {
		lines = append(lines, renderClause(s, baseIndent, width)...)
	}
	return lines
}

func fitsInline(segs []clauseSeg, baseIndent, width int) bool {
	total := 0
	for _, s := range segs {
		if s.name == "from" {
			for _, t := range s.body {
				if t.Kind == TokKeyword && t.Lower == "join" {
					return false
				}
			}
		}
		if s.name == "select" || s.name == "group by" || s.name == "order by" || s.name == "returning" {
			if len(splitTopLevelComma(trimTokens(s.body))) > 1 {
				return false
			}
		}
		total += len(s.name) + 1 + len(flatJoin(trimTokens(s.body))) + 1
	}
	return baseIndent+total <= targetWidth
}

func renderClause(s clauseSeg, baseIndent, width int) []string {
	kwCol := baseIndent
	pad := width - len(s.name)
	kwText := strings.Repeat(" ", pad) + s.name
	bodyCol := baseIndent + width + 1
	body := trimTokens(s.body)

	var bodyLines []string
	switch s.name {
	case "select", "group by", "order by", "returning", "values":
		bodyLines = layoutCommaList(body, bodyCol)
	case "where":
		bodyLines = layoutPredicateList(body, baseIndent+width)
	case "having":
		bodyLines = layoutPredicateList(body, baseIndent+width)
	case "from":
		bodyLines = layoutFrom(body, baseIndent, width)
	default:
		bodyLines = renderRun(body, bodyCol)
	}

	first := strings.Repeat(" ", kwCol) + kwText + " " + bodyLines[0]
	out := []string{first}
	out = append(out, bodyLines[1:]...)
	return out
}

// layoutFrom renders a FROM clause: the first table stays on the FROM line,
// each JOIN starts a new line. Multiple AND-ed ON predicates are right
// aligned to the join-keyword-phrase's own end column.
//
// width is the same river width riverWidth already computed for every
// other clause at this level (select/from/where/...), which by
// construction is wide enough to fit the longest JOIN phrase here too (see
// riverWidth) -- layoutFrom must reuse it as-is rather than recomputing its
// own, or its JOIN lines would align to a different column than the "from"
// keyword itself actually prints at.
func layoutFrom(toks []Token, baseIndent, width int) []string {
	segs := splitJoinSegments(toks)

	joinCol := baseIndent + width + 1
	phraseEndCol := joinCol - 1
	firstLines := renderRun(trimTokens(segs[0]), joinCol)
	out := firstLines

	for _, seg := range segs[1:] {
		seg = trimTokens(seg)
		if len(seg) == 0 {
			continue
		}
		kEnd := joinKeywordEnd(seg)
		phrase := flatJoin(seg[:kEnd])
		rest := seg[kEnd:]

		// split rest into table part and ON/USING condition.
		onIdx := -1
		d := 0
		for i, t := range rest {
			if t.Text == "(" {
				d++
			} else if t.Text == ")" {
				d--
			} else if d == 0 && t.Kind == TokKeyword && t.Lower == "on" {
				onIdx = i
				break
			}
		}
		phrasePad := phraseEndCol - len(phrase)
		if phrasePad < 0 {
			phrasePad = 0
		}
		if onIdx == -1 {
			line := strings.Repeat(" ", phrasePad) + phrase + " " + flatJoin(rest)
			out = append(out, line)
			continue
		}
		tablePart := trimTokens(rest[:onIdx])
		condPart := trimTokens(rest[onIdx+1:])
		preds, ops := splitAndOr(condPart)
		if len(preds) == 1 {
			// Single-condition ON stays inline after the join keyword phrase.
			line := strings.Repeat(" ", phrasePad) + phrase + " " + flatJoin(tablePart) + " on " + flatJoin(preds[0])
			out = append(out, line)
			continue
		}
		line := strings.Repeat(" ", phrasePad) + phrase + " " + flatJoin(tablePart)
		out = append(out, line)
		for idx, p := range preds {
			var kw string
			if idx == 0 {
				kw = "on"
			} else {
				kw = ops[idx-1]
			}
			pad := phraseEndCol - len(kw)
			if pad < 0 {
				pad = 0
			}
			pLines := renderRun(trimTokens(p), phraseEndCol+1)
			out = append(out, strings.Repeat(" ", pad)+kw+" "+pLines[0])
			out = append(out, pLines[1:]...)
		}
	}
	return out
}

// parseCTEs parses the "with [recursive] name as ( ... ), ..." prologue,
// returning each CTE's tokens (name as (body)) and the remaining query.
func parseCTEs(toks []Token) ([][]Token, []Token) {
	i := 1 // skip "with"
	if i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "recursive" {
		i++
	}
	var ctes [][]Token
	for i < len(toks) {
		start := i
		// name
		i++
		// optional output-column list: name(col, ...) as (...) -- only a
		// column list if what follows its matching ")" is "as"; otherwise
		// this "(" is the CTE body itself with no explicit "as".
		if i < len(toks) && toks[i].Text == "(" {
			close := matchParen(toks, i)
			if close+1 < len(toks) && toks[close+1].Kind == TokKeyword && toks[close+1].Lower == "as" {
				i = close + 1
			}
		}
		// optional "as"
		if i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "as" {
			i++
		}
		if i < len(toks) && toks[i].Text == "(" {
			close := matchParen(toks, i)
			i = close + 1
		}
		ctes = append(ctes, toks[start:i])
		if i < len(toks) && toks[i].Text == "," {
			i++
			continue
		}
		break
	}
	return ctes, toks[i:]
}

func renderCTE(cte []Token, baseIndent int, more bool) []string {
	name := renderTokenText(cte[0])
	i := 1
	// optional output-column list: name(col, ...) as (...)
	if i < len(cte) && cte[i].Text == "(" {
		colClose := matchParen(cte, i)
		if colClose+1 < len(cte) && cte[colClose+1].Kind == TokKeyword && cte[colClose+1].Lower == "as" {
			name += flatJoin(cte[i : colClose+1])
			i = colClose + 1
		}
	}
	if i < len(cte) && cte[i].Kind == TokKeyword && cte[i].Lower == "as" {
		i++
	}
	open := -1
	if i < len(cte) && cte[i].Text == "(" {
		open = i
	}
	if open == -1 {
		return []string{strings.Repeat(" ", baseIndent) + name}
	}
	close := matchParen(cte, open)
	inner := cte[open+1 : close]
	bodyIndent := baseIndent + 2
	bodyLines := formatQuerySegment(inner, bodyIndent)
	var lines []string
	lines = append(lines, strings.Repeat(" ", baseIndent)+name+" as (")
	for _, l := range bodyLines {
		lines = append(lines, strings.Repeat(" ", bodyIndent)+l)
	}
	closing := strings.Repeat(" ", baseIndent) + ")"
	if more {
		closing += ","
	}
	lines = append(lines, closing)
	return lines
}
