package format

import "strings"

const targetWidth = 78

// clauseWords lists the recognized clause-starting keyword sequences, in the
// order they're checked. Two-word entries must be checked before any
// single-word entry that shares their first word.
var clauseWords = []struct {
	words []string
	name  string
	// leadingOnly restricts the match to position 0 of the statement.
	// EXPLAIN is only ever a statement prefix in PostgreSQL, so matching
	// it anywhere else would split "select explain from t" -- a query
	// against a column that happens to be named "explain" -- into two
	// clauses.
	leadingOnly bool
}{
	{[]string{"explain"}, "explain", true},
	{[]string{"insert", "into"}, "insert into", false},
	{[]string{"on", "conflict"}, "on conflict", false},
	{[]string{"group", "by"}, "group by", false},
	{[]string{"order", "by"}, "order by", false},
	{[]string{"select"}, "select", false},
	{[]string{"from"}, "from", false},
	{[]string{"where"}, "where", false},
	{[]string{"having"}, "having", false},
	{[]string{"update"}, "update", false},
	{[]string{"set"}, "set", false},
	{[]string{"delete"}, "delete", false},
	{[]string{"returning"}, "returning", false},
	{[]string{"values"}, "values", false},
	{[]string{"limit"}, "limit", false},
	{[]string{"offset"}, "offset", false},
}

type clauseSeg struct {
	name string
	body []Token
	// kwTok is the clause keyword's own first token (e.g. "from", "group"
	// for "group by"). Kept around solely so a comment attached to it --
	// which happens whenever a leading comment sits on its own line(s)
	// directly before the keyword, rather than before the first token of
	// its body -- isn't silently unreachable; body alone doesn't cover
	// that position.
	kwTok Token
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

// splitUnionSegments splits toks on every top-level UNION/UNION ALL/
// INTERSECT/EXCEPT, returning each query segment and, between consecutive
// segments, the operator text that joins them ("union", "union all",
// "intersect", "except"). Each segment is independently formatted (its own
// select/from/where/... clauses, its own river-alignment width) by the
// caller, since combining two SELECTs' clause lists into one shared width
// the way a single query's own clauses are would be meaningless -- they're
// not the same query.
func splitUnionSegments(toks []Token) (segs [][]Token, ops []string) {
	depth := 0
	start := 0
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
		switch t.Lower {
		case "union":
			end := i + 1
			op := "union"
			if end < len(toks) && toks[end].Kind == TokKeyword && toks[end].Lower == "all" {
				op = "union all"
				end++
			}
			segs = append(segs, toks[start:i])
			ops = append(ops, op)
			start = end
			i = end - 1
		case "intersect":
			segs = append(segs, toks[start:i])
			ops = append(ops, "intersect")
			start = i + 1
		case "except":
			segs = append(segs, toks[start:i])
			ops = append(ops, "except")
			start = i + 1
		}
	}
	segs = append(segs, toks[start:])
	return segs, ops
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
			if cw.leadingOnly && i != 0 {
				continue
			}
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
		segs = append(segs, clauseSeg{name: b.name, body: toks[b.kwEnd:end], kwTok: toks[b.idx]})
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
		// plainJoin: measurement only, see fitsInline's comment on why not
		// flatJoin.
		if n := len(plainJoin(seg[:joinKeywordEnd(seg)])); n > max {
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
// spaceBetween decides whether a space belongs between prev and next as
// they're rendered in sequence. prevPrev -- the token before prev, or nil
// at the start of a run -- exists solely to tell a unary "-"/"+" (no space
// before its operand: "-0.12", not "- 0.12") apart from the binary
// operator (a space on both sides: "a - b"): prev alone can't distinguish
// them, since "-"/"+" render identically as tokens either way.
func spaceBetween(prevPrev *Token, prev, next Token) bool {
	if (prev.Text == "-" || prev.Text == "+") && isUnaryContext(prevPrev) {
		return false
	}
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
	if next.Text == "[" {
		// array subscript/slice ("arr[1]", "(pos)[0]"): never a space
		// before "[" regardless of what precedes it.
		return false
	}
	return true
}

// isUnaryContext reports whether a "-"/"+" immediately following prevPrev
// (nil at the very start of a run) is acting as a unary sign rather than a
// binary operator: true unless prevPrev is a token that just completed a
// value (an identifier, literal, or closing ")"/"]"), in which case "-"/"+"
// is subtracting/adding from it.
func isUnaryContext(prevPrev *Token) bool {
	if prevPrev == nil {
		return true
	}
	switch prevPrev.Kind {
	case TokIdent, TokNumber, TokString, TokDollarString, TokParam:
		return false
	}
	if prevPrev.Text == ")" || prevPrev.Text == "]" {
		return false
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

	var prev, prevPrev *Token
	i := 0
	for i < len(toks) {
		t := toks[i]
		if isNonLayout(t) {
			if prev != nil {
				write(" ")
			}
			write(t.Text)
			prevPrev, prev = prev, &toks[i]
			i++
			continue
		}

		// Safety net: a token whose leading comment wasn't already
		// consumed by an outer per-item wrapper (layoutCommaList,
		// layoutPredicateList, ...) -- because it's buried inside a
		// sub-expression those don't look inside, e.g. a function-call
		// argument -- still needs somewhere safe to go, rather than being
		// silently dropped (the previous behavior) or glued onto whatever
		// text precedes it on the current line. Not attempting precise
		// fidelity here (this path is rare); just guaranteed-safe: each
		// leading comment on its own line, at whatever column the current
		// line is already at.
		if len(toks[i].Comments) > 0 {
			indent := curCol
			lead := renderLeadingComments(toks[i].Comments, indent)
			toks[i].Comments = nil
			if strings.TrimSpace(lines[len(lines)-1]) == "" {
				lines[len(lines)-1] = lead[0]
				lead = lead[1:]
			}
			lines = append(lines, lead...)
			lines = append(lines, strings.Repeat(" ", indent))
			curCol = indent
			prev = nil
		}
		t = toks[i]

		if t.Kind == TokKeyword && t.Lower == "case" {
			end := matchCaseEnd(toks, i)
			if prev != nil && spaceBetween(prevPrev, *prev, t) {
				write(" ")
			}
			merge(layoutCase(toks[i:end+1], curCol))
			prevPrev, prev = prev, &toks[end]
			i = end + 1
			continue
		}

		if t.Kind == TokKeyword && t.Lower == "over" && i+1 < len(toks) && toks[i+1].Text == "(" {
			close := matchParen(toks, i+1)
			overToks := toks[i : close+1]
			if prev != nil && spaceBetween(prevPrev, *prev, t) {
				write(" ")
			}
			flat := plainJoin(overToks)
			if curCol+len(flat) <= targetWidth {
				write(flat)
			} else {
				merge(layoutOver(overToks, curCol))
			}
			prevPrev, prev = prev, &toks[close]
			i = close + 1
			continue
		}

		if t.Text == "(" {
			close := matchParen(toks, i)
			inner := toks[i+1 : close]
			needSpace := prev != nil && spaceBetween(prevPrev, *prev, t)
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
			prevPrev, prev = prev, &toks[close]
			i = close + 1
			continue
		}

		if prev != nil && spaceBetween(prevPrev, *prev, t) {
			write(" ")
		}
		write(renderTokenText(t))
		// Safety net, mirroring the leading-comment one above: a trailing
		// comment not already consumed by an outer per-item wrapper still
		// forces a line break here (a "--" comment silently absorbing
		// whatever token would otherwise follow it on the same line is
		// exactly the corruption bug this whole mechanism exists to avoid).
		if toks[i].TrailingComment != nil {
			write(commentMarker + trailingCommentText(toks[i].TrailingComment))
			toks[i].TrailingComment = nil
			lines = append(lines, "")
			curCol = 0
		}
		prevPrev, prev = prev, &toks[i]
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
	var prev, prevPrev *Token
	for i := range toks {
		t := toks[i]
		if isNonLayout(t) {
			continue
		}
		if prev != nil && spaceBetween(prevPrev, *prev, t) {
			sb.WriteByte(' ')
		}
		sb.WriteString(renderTokenText(t))
		prevPrev, prev = prev, &toks[i]
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

	// toks[0] == "case". A *simple* CASE puts an operand expression between
	// "case" and the first "when" ("case grouping(x) when 1 then ..."); a
	// *searched* CASE goes straight to "when". Consume the operand, if
	// there is one, onto the "case" line: without this the when-loop below
	// -- which only advances while it is looking at a "when" -- never
	// starts, i stays at 1, and every token from the operand up to "end"
	// is silently dropped, rendering the whole expression as "case end".
	whenCol := caseCol + len("case ")
	i := 1
	head := "case"
	if opEnd := caseOperandEnd(toks); opEnd > 1 {
		head += " " + plainJoin(toks[1:opEnd])
		i = opEnd
	}
	lines := []string{head}
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

// caseOperandEnd returns the index of the first top-level "when" in a CASE
// token run, i.e. the end of a simple CASE's operand expression. It returns
// 1 for a searched CASE ("case when ..."), where there is no operand.
func caseOperandEnd(toks []Token) int {
	depth := 0
	for i := 1; i < len(toks); i++ {
		switch {
		case toks[i].Text == "(":
			depth++
		case toks[i].Text == ")":
			depth--
		case depth == 0 && toks[i].Kind == TokKeyword && toks[i].Lower == "when":
			return i
		}
	}
	return 1
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
	// Comment check first, and short-circuits before flatJoin ever runs:
	// flatJoin's underlying renderRun call consumes/clears any comment
	// metadata on these tokens as a side effect, which the multi-line path
	// below still needs intact if this fast path doesn't end up taking it.
	if len(items) > 0 && !anyItemComments(items) {
		if flat := flatJoin(trimTokens(toks)); startCol+len(flat) <= targetWidth {
			return []string{flat}
		}
	}
	var lines []string
	for idx, it := range items {
		it = trimTokens(it)
		// Cleared before renderRun runs, not after: renderRun has its own
		// safety-net handling for a token whose comment metadata is still
		// set (for tokens no per-item wrapper reaches), and calling it
		// after would make that safety net fire redundantly on the very
		// comment this loop is about to render properly itself.
		lead := leadingCommentLines(it, startCol)
		trailing := trailingCommentSuffix(it)
		itLines := renderRun(it, startCol)
		if idx < len(items)-1 {
			// The separating comma belongs on the item's own last rendered
			// line, not necessarily its first -- an item that itself wraps
			// (e.g. a long CASE expression) has already produced several
			// lines by this point.
			itLines[len(itLines)-1] += ","
		}
		itLines[len(itLines)-1] += trailing
		if idx == 0 {
			lines = append(lines, lead...)
			lines = append(lines, itLines[0])
		} else {
			lines = append(lines, lead...)
			lines = append(lines, strings.Repeat(" ", startCol)+itLines[0])
		}
		lines = append(lines, itLines[1:]...)
	}
	return lines
}

// anyTokenComments reports whether any token in toks carries a leading or
// trailing comment -- used to force a multi-line layout even when a
// flattened single-line render would otherwise fit, since that fast path
// has nowhere to put a comment.
func anyTokenComments(toks []Token) bool {
	for _, t := range toks {
		if len(t.Comments) > 0 || t.TrailingComment != nil {
			return true
		}
	}
	return false
}

// anyItemComments reports whether any item in a comma/predicate/join list
// carries a leading or trailing comment -- used to force the multi-line
// layout even when the flattened form would otherwise fit inline, since the
// single-line fast path has nowhere to put a comment.
func anyItemComments(items [][]Token) bool {
	for _, it := range items {
		it = trimTokens(it)
		if len(it) == 0 {
			continue
		}
		if len(it[0].Comments) > 0 || it[len(it)-1].TrailingComment != nil {
			return true
		}
	}
	return false
}

// leadingCommentLines renders any leading comments on item's first token as
// complete lines at indent -- for the caller to splice in before its own
// prefix+first-content-line composition.
func leadingCommentLines(item []Token, indent int) []string {
	item = trimTokens(item)
	if len(item) == 0 || len(item[0].Comments) == 0 {
		return nil
	}
	lines := renderLeadingComments(item[0].Comments, indent)
	// Cleared once rendered here, so renderRun's own safety-net handling
	// (for comments on tokens no per-item wrapper reaches, e.g. deep inside
	// a function call) never double-renders what this call already emitted.
	item[0].Comments = nil
	return lines
}

// trailingCommentSuffix returns commentMarker+text for item's last token's
// trailing comment, if any, else "" -- appended to whatever line ends up
// holding that token; alignTrailingComments pads it later. Clears the field
// once read, for the same reason leadingCommentLines does.
func trailingCommentSuffix(item []Token) string {
	item = trimTokens(item)
	if len(item) == 0 {
		return ""
	}
	last := &item[len(item)-1]
	if last.TrailingComment == nil {
		return ""
	}
	text := commentMarker + trailingCommentText(last.TrailingComment)
	last.TrailingComment = nil
	return text
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
		// Cleared before renderRun runs -- see layoutCommaList's identical
		// ordering concern.
		lead := leadingCommentLines(p, startCol)
		trailing := trailingCommentSuffix(p)
		pLines := renderRun(p, startCol)
		pLines[len(pLines)-1] += trailing
		// idx 0's line is appended directly after the clause keyword by the
		// caller (renderClause, which also strips its leading comment), so
		// it carries no prefix of its own here; only continuation lines get
		// a right-aligned AND/OR prefix and their own leading comments.
		if idx == 0 {
			lines = append(lines, pLines...)
			continue
		}
		kw := ops[idx-1]
		pad := endCol - len(kw)
		if pad < 0 {
			pad = 0
		}
		lines = append(lines, lead...)
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
		withPrefix := "with "
		if len(toks) > 1 && toks[1].Kind == TokKeyword && toks[1].Lower == "recursive" {
			withPrefix = "with recursive "
		}
		ctes, rest := parseCTEs(toks)
		var lines []string
		for idx, c := range ctes {
			prefix := ""
			if idx == 0 {
				prefix = withPrefix
			}
			lines = append(lines, renderCTE(c, baseIndent, idx < len(ctes)-1, prefix)...)
		}
		lines = append(lines, formatQuerySegment(rest, baseIndent)...)
		return lines
	}

	if usegs, uops := splitUnionSegments(toks); len(usegs) > 1 {
		var lines []string
		for idx, seg := range usegs {
			lines = append(lines, formatQuerySegment(seg, baseIndent)...)
			if idx < len(uops) {
				lines = append(lines, strings.Repeat(" ", baseIndent)+uops[idx])
			}
		}
		return lines
	}

	segs := splitClauses(toks)
	if segs == nil {
		return []string{flatJoin(toks)}
	}
	width := riverWidth(segs)

	if fitsInline(segs, baseIndent, width) && !anyTokenComments(toks) {
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
		// STYLE.md rule 19: the EXPLAIN prefix keeps a line of its own even
		// when everything would otherwise fit on one, so the statement being
		// explained still reads as a statement rather than as an argument.
		if s.name == "explain" {
			return false
		}
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
		// plainJoin, not flatJoin: this is a width estimate whose returned
		// string is discarded, and flatJoin's underlying renderRun call has
		// a side effect (consuming/clearing any comment metadata on these
		// tokens) that a later, real render pass over the same tokens
		// still needs intact.
		total += len(s.name) + 1 + len(plainJoin(trimTokens(s.body))) + 1
	}
	return baseIndent+total <= targetWidth
}

func renderClause(s clauseSeg, baseIndent, width int) []string {
	kwCol := baseIndent
	pad := width - len(s.name)
	kwText := strings.Repeat(" ", pad) + s.name
	bodyCol := baseIndent + width + 1
	body := trimTokens(s.body)

	// A leading comment on the clause keyword's own token (e.g. it sits on
	// its own line directly before "from") or on the clause body's very
	// first token would otherwise get glued onto the "select "/"from "/etc.
	// keyword line by the concatenation below (bodyLines[0] is assumed to
	// be real content). Strip and render it separately, at the same column
	// the body's own content will start at, before that concatenation
	// happens. Both positions matter: the keyword-token case is what a
	// second formatting pass over this package's own output produces,
	// since by then the comment already sits on its own line right before
	// the keyword rather than before the first body token.
	var lead []string
	switch {
	case len(s.kwTok.Comments) > 0:
		lead = renderLeadingComments(s.kwTok.Comments, bodyCol)
	case len(body) > 0 && len(body[0].Comments) > 0:
		lead = renderLeadingComments(body[0].Comments, bodyCol)
		body[0].Comments = nil
	}

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
	// A clause with an empty body -- "explain" with no option list is the
	// only one in practice -- would otherwise leave a trailing space.
	first = strings.TrimRight(first, " ")
	out := append(lead, first)
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
	// Cleared before renderRun runs -- see layoutCommaList's identical
	// ordering concern.
	firstTrailing := trailingCommentSuffix(segs[0])
	firstLines := renderRun(trimTokens(segs[0]), joinCol)
	firstLines[len(firstLines)-1] += firstTrailing
	out := firstLines

	for _, seg := range segs[1:] {
		seg = trimTokens(seg)
		if len(seg) == 0 {
			continue
		}
		out = append(out, leadingCommentLines(seg, joinCol)...)
		segTrailing := trailingCommentSuffix(seg)
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
			line := strings.Repeat(" ", phrasePad) + phrase + " " + flatJoin(rest) + segTrailing
			out = append(out, line)
			continue
		}
		tablePart := trimTokens(rest[:onIdx])
		condPart := trimTokens(rest[onIdx+1:])
		preds, ops := splitAndOr(condPart)
		if len(preds) == 1 {
			// Single-condition ON stays inline after the join keyword phrase.
			line := strings.Repeat(" ", phrasePad) + phrase + " " + flatJoin(tablePart) + " on " + flatJoin(preds[0]) + segTrailing
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
			if idx == len(preds)-1 {
				pLines[len(pLines)-1] += segTrailing
			}
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

// renderCTE renders one "name [(cols)] as ( body )" CTE entry. withPrefix,
// when non-empty ("with "), is prepended to the CTE's own "name as (" line
// -- never to a leading comment line that might precede it -- since only
// the first CTE in a WITH prologue carries it.
func renderCTE(cte []Token, baseIndent int, more bool, withPrefix string) []string {
	lead := renderLeadingComments(cte[0].Comments, baseIndent)
	cte[0].Comments = nil
	name := withPrefix + renderTokenText(cte[0])
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
		return append(lead, strings.Repeat(" ", baseIndent)+name)
	}
	close := matchParen(cte, open)
	inner := cte[open+1 : close]
	bodyIndent := baseIndent + 2
	bodyLines := formatQuerySegment(inner, bodyIndent)
	lines := lead
	lines = append(lines, strings.Repeat(" ", baseIndent)+name+" as (")
	for _, l := range bodyLines {
		lines = append(lines, strings.Repeat(" ", bodyIndent)+l)
	}
	closing := strings.Repeat(" ", baseIndent) + ")"
	if more {
		closing += ","
	}
	closing += trailingCommentSuffix(cte)
	lines = append(lines, closing)
	return lines
}
