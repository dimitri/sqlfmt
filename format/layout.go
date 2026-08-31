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
		case "union", "intersect", "except":
			// All three take an optional ALL or DISTINCT modifier, and it
			// belongs to the operator, not to the query that follows it.
			// Only UNION used to consume it: "except all select ..." left
			// "all" as the first token of the next segment, which rendered
			// as a stray "all select ..." line -- and, in a segment the
			// layout then recursed into, could be dropped outright. Losing
			// it silently turns EXCEPT ALL into EXCEPT, which is a
			// different result set, not a formatting difference.
			end := i + 1
			op := t.Lower
			if end < len(toks) && toks[end].Kind == TokKeyword &&
				(toks[end].Lower == "all" || toks[end].Lower == "distinct") {
				op += " " + toks[end].Lower
				end++
			}
			segs = append(segs, toks[start:i])
			ops = append(ops, op)
			start = end
			i = end - 1
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

// joinTableLines renders a JOIN's table expression, keeping it multi-line
// when it is one. flatJoin was used here, and flatJoin joins renderRun's
// output with spaces -- so a LATERAL subquery, which renderRun lays out
// across several lines, was folded back onto the JOIN line and ran to
// hundreds of columns. Always returns at least one line.
func joinTableLines(toks []Token, col int) []string {
	lines := renderRun(trimTokens(toks), col)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
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
// fillCommaList packs comma-separated items onto as few lines as fit
// within targetWidth, each continuation line starting at col. Unlike
// layoutCommaList, which gives every item its own line, this keeps a long
// list of short items compact -- an INSERT column list or a VALUES row
// reads as a list, not as a column of one-word lines.
//
// It reports ok=false when filling cannot help: if col is already so deep
// that even a single item overflows, breaking the list only adds ragged
// lines to an over-long one, and the caller should leave it alone.
// layoutInsertTarget renders "insert into t (col, col, ...)". The column
// list is not a function call's argument list, but it looks exactly like
// one -- it follows an identifier directly -- so renderRun's paren handling
// declines to break it, per rule 4, and a wide list ran off the page. Only
// the clause knows better, so the fill happens here.
//
// ok is false when there is no column list, or when it already fits.
func layoutInsertTarget(body []Token, bodyCol int) ([]string, bool) {
	body = trimTokens(body)
	if len(body) < 3 || body[len(body)-1].Text != ")" {
		return nil, false
	}
	open := -1
	depth := 0
	for i := len(body) - 1; i >= 0; i-- {
		switch body[i].Text {
		case ")":
			depth++
		case "(":
			depth--
			if depth == 0 {
				open = i
			}
		}
		if open >= 0 {
			break
		}
	}
	if open <= 0 {
		return nil, false
	}
	head := plainJoin(body[:open])
	flat := head + plainJoin(body[open:])
	if bodyCol+len(flat) <= targetWidth {
		return nil, false
	}
	filled, ok := fillCommaList(body[open+1:len(body)-1], bodyCol+len(head)+1)
	if !ok {
		return nil, false
	}
	filled[0] = head + "(" + filled[0]
	filled[len(filled)-1] += ")"
	return filled, true
}

// layoutRowAssignment renders the multiple-column form of UPDATE ... SET,
// "set (a, b, ...) = (x, y, ...)", when it does not fit on one line. Both
// sides are parenthesized lists that routinely run to a couple of hundred
// characters between them, and there is no column deep inside the second
// list from which breaking helps -- the break has to be at the "=", which
// is a clause-level decision renderRun cannot make from inside the
// expression:
//
//	set (name, bio, nationality, gender, begin, "end", wiki_qid, ulan)
//	  = (batch.name, batch.bio, batch.nationality, batch.gender,
//	     batch.begin, batch."end", batch.wiki_qid, batch.ulan)
//
// The "=" is right-aligned into the clause river, the same way
// layoutPredicateList aligns a continuation AND/OR under its clause
// keyword. Each side is then filled across lines if it needs to be.
//
// ok is false when this is not the row form, or when it already fits.
func layoutRowAssignment(body []Token, baseIndent, width int) ([]string, bool) {
	body = trimTokens(body)
	eq := topLevelRowOp(body)
	if eq < 0 {
		return nil, false
	}
	op := body[eq].Lower
	lhs, rhs := trimTokens(body[:eq]), trimTokens(body[eq+1:])
	if !isParenGroup(lhs) || !isParenGroup(rhs) {
		return nil, false
	}
	bodyCol := baseIndent + width + 1
	if bodyCol+len(plainJoin(body)) <= targetWidth {
		return nil, false // fits as it is
	}

	side := func(g []Token, col int) []string {
		flat := plainJoin(g)
		if col+len(flat) <= targetWidth {
			return []string{flat}
		}
		if filled, ok := fillCommaList(g[1:len(g)-1], col+1); ok {
			filled[0] = "(" + filled[0]
			filled[len(filled)-1] += ")"
			return filled
		}
		return []string{flat}
	}

	lines := side(lhs, bodyCol)
	opPad := width - len(op)
	if opPad < 0 {
		opPad = 0
	}
	rhsLines := side(rhs, bodyCol)
	rhsLines[0] = strings.Repeat(" ", baseIndent+opPad) + op + " " + rhsLines[0]
	return append(lines, rhsLines...), true
}

// rowCompareOps are the operators that can sit between two parenthesized
// row constructors and therefore make a sensible break point.
var rowCompareOps = map[string]bool{
	"=": true, "<>": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true,
	"is": true, "in": true,
}

// topLevelRowOp returns the index of a top-level row-comparison operator,
// or -1.
func topLevelRowOp(toks []Token) int {
	depth := 0
	for i, t := range toks {
		switch {
		case t.Text == "(":
			depth++
		case t.Text == ")":
			depth--
		case depth == 0 && rowCompareOps[t.Lower]:
			return i
		}
	}
	return -1
}

// isParenGroup reports whether toks is exactly one parenthesized group.
func isParenGroup(toks []Token) bool {
	return len(toks) >= 2 && toks[0].Text == "(" && matchParen(toks, 0) == len(toks)-1
}

func fillCommaList(inner []Token, col int) (lines []string, ok bool) {
	items := splitTopLevelComma(trimTokens(inner))
	if len(items) < 2 {
		return nil, false
	}
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = plainJoin(trimTokens(it))
		if col+len(texts[i]) > targetWidth {
			return nil, false
		}
	}
	cur := ""
	for i, t := range texts {
		piece := t
		if i < len(texts)-1 {
			piece += ","
		}
		switch {
		case cur == "":
			cur = piece
		case col+len(cur)+1+len(piece) <= targetWidth:
			cur += " " + piece
		default:
			lines = append(lines, cur)
			cur = piece
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) < 2 {
		return nil, false // it fitted after all; nothing gained
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", col) + lines[i]
	}
	return lines, true
}

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
				// A standalone parenthesized comma list that overflows --
				// an INSERT column list, a VALUES row, the sides of
				// "set (a, ...) = (x, ...)" -- is filled across lines,
				// aligned just inside its own paren. needSpace excludes a
				// function call's argument list (rule 4: no space before
				// its "("), which the corpus keeps on one line. fill
				// declines when the paren sits too deep for breaking to
				// help, leaving the flat form rather than a ragged one.
				if needSpace && len(content) == 1 &&
					curCol+len(content[0]) > targetWidth {
					if filled, fok := fillCommaList(inner, curCol+1); fok {
						write("(")
						merge(filled)
						write(")")
						prevPrev, prev = prev, &toks[close]
						i = close + 1
						continue
					}
				}
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

// layoutOver renders a long OVER(...) with PARTITION BY, ORDER BY, and any
// frame clause each on its own line under the opening paren's column, per
// STYLE.md rule 14.
//
// splitTopLevelTwoWord returns [before, after]. The ORDER BY search used to
// run over segs[0] -- the tokens *before* PARTITION BY, which for the usual
// "over(partition by x order by y)" is empty -- so it never matched: the
// whole body went out on the "partition by" line and only the ")" moved
// down, giving a 90+ column line with an orphaned paren under it.
// Everything after PARTITION BY has to be searched instead.
func layoutOver(toks []Token, overCol int) []string {
	openCol := overCol + len("over(")
	// toks: over ( ... )
	inner := toks[2 : len(toks)-1]

	// "over(" keeps a line of its own, with every window clause under the
	// opening paren's column -- the shape the corpus already uses.
	lines := []string{"over("}
	emit := func(prefix string, body []Token) {
		body = trimTokens(body)
		if len(body) == 0 {
			return
		}
		bodyLines := renderRun(body, openCol+len(prefix))
		lines = append(lines, strings.Repeat(" ", openCol)+prefix+strings.TrimSpace(bodyLines[0]))
		lines = append(lines, bodyLines[1:]...)
	}

	rest := inner
	if segs := splitTopLevelTwoWord(inner, "partition", "by"); len(segs) == 2 {
		// segs[1] is everything after "partition by": the partition
		// expression list, then any ORDER BY and frame clause.
		partition, tail := segs[1], []Token(nil)
		if o := splitTopLevelTwoWord(segs[1], "order", "by"); len(o) == 2 {
			partition, tail = o[0], o[1]
			emit("partition by ", partition)
			emit("order by ", frameSplit(&tail))
			rest = tail
		} else {
			partition, rest = splitFrame(segs[1])
			emit("partition by ", partition)
		}
	} else if o := splitTopLevelTwoWord(inner, "order", "by"); len(o) == 2 {
		tail := o[1]
		emit("order by ", frameSplit(&tail))
		rest = tail
	} else {
		body, frame := splitFrame(inner)
		emit("", body)
		rest = frame
	}
	// Whatever is left is the frame clause ("rows between ...",
	// "range ...", "groups ...", with an optional "exclude ...").
	emit("", rest)

	lines = append(lines, strings.Repeat(" ", overCol)+")")
	return lines
}

// frameStarters are the keywords that begin a window frame clause, which
// rule 14 puts on a line of its own after PARTITION BY / ORDER BY.
var frameStarters = map[string]bool{"rows": true, "range": true, "groups": true}

// splitFrame splits a window-clause token run at the start of its frame
// clause, returning the part before it and the frame itself (nil when there
// is no frame).
func splitFrame(toks []Token) (body, frame []Token) {
	depth := 0
	for i, t := range toks {
		switch {
		case t.Text == "(":
			depth++
		case t.Text == ")":
			depth--
		case depth == 0 && t.Kind == TokKeyword && frameStarters[t.Lower]:
			return toks[:i], toks[i:]
		}
	}
	return toks, nil
}

// frameSplit peels the frame clause off *tail, leaving *tail holding just
// the frame and returning the part before it -- a small helper so the
// ORDER BY body and the frame can be emitted as two separate lines.
func frameSplit(tail *[]Token) []Token {
	body, frame := splitFrame(*tail)
	*tail = frame
	return body
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
// hoistSegmentComments strips every attached comment off toks (in place)
// and returns them rendered as their own lines at indent. Used where a
// caller is about to flatten a token run to a single line and would
// otherwise inline them.
func hoistSegmentComments(toks []Token, indent int) []string {
	var out []string
	for i := range toks {
		if len(toks[i].Comments) > 0 {
			out = append(out, renderLeadingComments(toks[i].Comments, indent)...)
			toks[i].Comments = nil
		}
	}
	return out
}

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
		// A row comparison, "(a, b, ...) <> (x, y, ...)", has the same
		// shape problem as the SET row form: no column inside the second
		// list is a useful break point, the operator is.
		// baseIndent 0 / width endCol puts the operator's own right edge at
		// endCol, the column the clause keyword and any AND/OR end at.
		if len(pLines) == 1 && startCol+len(pLines[0]) > targetWidth {
			if l, ok := layoutRowAssignment(trimTokens(p), 0, endCol); ok {
				pLines = l
			}
		}
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
			// The "and" in "between X and Y" joins the two bounds of one
			// predicate; it is not a boolean conjunction, and splitting on
			// it turned "where y between 2010 and 2017" into two lines that
			// read as two separate conditions.
			if t.Lower == "and" && betweenAndAt(toks, start, i) {
				continue
			}
			preds = append(preds, toks[start:i])
			ops = append(ops, t.Lower)
			start = i + 1
		}
	}
	preds = append(preds, toks[start:])
	return preds, ops
}

// wholeParen reports whether toks is exactly one parenthesized group, and
// if so returns its contents.
func wholeParen(toks []Token) ([]Token, bool) {
	toks = trimTokens(toks)
	if len(toks) < 3 || toks[0].Text != "(" {
		return nil, false
	}
	if matchParen(toks, 0) != len(toks)-1 {
		return nil, false
	}
	return trimTokens(toks[1 : len(toks)-1]), true
}

// betweenAndAt reports whether the "and" at index i is the one belonging to
// a BETWEEN in the same predicate -- i.e. whether an unconsumed "between"
// appears between the start of the current predicate and that "and". A
// second BETWEEN inside the same predicate would each claim their own
// "and", so they are counted rather than merely detected.
func betweenAndAt(toks []Token, start, i int) bool {
	depth, pending := 0, 0
	for j := start; j < i; j++ {
		t := toks[j]
		switch {
		case t.Text == "(":
			depth++
		case t.Text == ")":
			depth--
		case depth != 0:
		case t.Kind == TokKeyword && t.Lower == "between":
			pending++
		case t.Kind == TokKeyword && t.Lower == "and" && pending > 0:
			pending--
		}
	}
	return pending > 0
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

	// A fully parenthesized query -- the arms of "(select ...) except
	// (select ...)" are written this way -- has every clause keyword at
	// depth 1, so splitClauses finds none and the whole arm used to come
	// back as one flat line, hundreds of columns wide. Unwrap, format the
	// query inside, and put the parens back.
	// "(select ...) order by x" -- a parenthesized arm with trailing
	// clauses of its own. Format the group, then the clauses after it.
	if len(toks) > 0 && toks[0].Text == "(" {
		if close := matchParen(toks, 0); close > 0 && close < len(toks)-1 {
			head := formatQuerySegment(toks[:close+1], baseIndent)
			return append(head, formatQuerySegment(toks[close+1:], baseIndent)...)
		}
	}
	if inner, ok := wholeParen(toks); ok {
		body := formatQuerySegment(inner, baseIndent+1)
		if len(body) > 0 {
			body[0] = strings.Repeat(" ", baseIndent) + "(" + strings.TrimLeft(body[0], " ")
			body = append(body, strings.Repeat(" ", baseIndent)+")")
			return body
		}
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
	case "insert into":
		if l, ok := layoutInsertTarget(body, bodyCol); ok {
			bodyLines = l
		} else {
			bodyLines = renderRun(body, bodyCol)
		}
	case "set":
		switch {
		case len(splitTopLevelComma(trimTokens(body))) > 1:
			// A multi-assignment SET is a comma list, and gets the same
			// one-item-per-line treatment as SELECT's. renderRun instead
			// walked it inline, so the first assignment that had to wrap
			// -- a CASE, typically -- did so from wherever it happened to
			// start, and every assignment after it began deeper still: four
			// CASE expressions cascaded into a 130-column staircase.
			bodyLines = layoutCommaList(body, bodyCol)
		default:
			if l, ok := layoutRowAssignment(body, baseIndent, width); ok {
				bodyLines = l
			} else {
				bodyLines = renderRun(body, bodyCol)
			}
		}
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
		// A comment attached to a token inside this segment -- a block
		// comment above the joined relation, say -- makes renderRun emit
		// several lines, and flatJoin below would glue them back into one
		// with the comment inlined, running to 200+ columns. Hoist those
		// comments out onto their own lines first, at the join column,
		// which is where a reader expects a comment about this join.
		if anyTokenComments(seg[kEnd:]) {
			out = append(out, hoistSegmentComments(seg[kEnd:], joinCol)...)
		}
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
			head := strings.Repeat(" ", phrasePad) + phrase + " "
			tl := joinTableLines(rest, len(head))
			tl[len(tl)-1] += segTrailing
			out = append(out, head+tl[0])
			out = append(out, tl[1:]...)
			continue
		}
		tablePart := trimTokens(rest[:onIdx])
		condPart := trimTokens(rest[onIdx+1:])
		preds, ops := splitAndOr(condPart)
		head := strings.Repeat(" ", phrasePad) + phrase + " "
		tl := joinTableLines(tablePart, len(head))
		if len(preds) == 1 {
			// Single-condition ON stays inline after the join keyword
			// phrase -- unless the table part is a subquery that wrapped,
			// in which case the ON goes under it.
			if len(tl) == 1 {
				out = append(out, head+tl[0]+" on "+flatJoin(preds[0])+segTrailing)
				continue
			}
			out = append(out, head+tl[0])
			out = append(out, tl[1:]...)
			pad := phraseEndCol - len("on")
			if pad < 0 {
				pad = 0
			}
			pLines := renderRun(trimTokens(preds[0]), phraseEndCol+1)
			pLines[len(pLines)-1] += segTrailing
			out = append(out, strings.Repeat(" ", pad)+"on "+pLines[0])
			out = append(out, pLines[1:]...)
			continue
		}
		out = append(out, head+tl[0])
		out = append(out, tl[1:]...)
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
		// optional "as", then the optional materialization hint
		// ("as materialized (" / "as not materialized ("). Without
		// consuming the hint the "(" test below failed, the CTE body was
		// never taken, and everything from "materialized" onwards fell out
		// into the tail -- which is how a MATERIALIZED CTE lost its body.
		if i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "as" {
			i++
			i = skipMaterialized(toks, i)
		}
		if i < len(toks) && toks[i].Text == "(" {
			close := matchParen(toks, i)
			i = close + 1
		}
		// A recursive CTE may be followed by SEARCH and/or CYCLE clauses,
		// which belong to it rather than to the statement after it. Left in
		// the tail they were parsed as part of the main query, where CYCLE's
		// own "set" reads as an UPDATE clause keyword and the clause got
		// mangled.
		i = skipSearchCycle(toks, i)
		ctes = append(ctes, toks[start:i])
		if i < len(toks) && toks[i].Text == "," {
			i++
			continue
		}
		break
	}
	return ctes, toks[i:]
}

// skipMaterialized advances past an optional "materialized" /
// "not materialized" hint following a CTE's "as".
func skipMaterialized(toks []Token, i int) int {
	if i < len(toks) && toks[i].Kind == TokKeyword && toks[i].Lower == "not" {
		if i+1 < len(toks) && toks[i+1].Lower == "materialized" {
			return i + 2
		}
		return i
	}
	if i < len(toks) && toks[i].Lower == "materialized" {
		return i + 1
	}
	return i
}

// skipSearchCycle advances past the SEARCH and CYCLE clauses a recursive
// CTE may carry, so they stay attached to the CTE they qualify:
//
//	search depth first by id set ordercol
//	cycle id set is_cycle using path
//
// Both run to the next "," or to the statement's main SELECT.
func skipSearchCycle(toks []Token, i int) int {
	// "search"/"cycle" are not in the keyword table -- they lex as plain
	// identifiers -- so match on the text, not on Kind.
	for i < len(toks) && (toks[i].Lower == "search" || toks[i].Lower == "cycle") {
		i++
		depth := 0
		for i < len(toks) {
			t := toks[i]
			if t.Text == "(" {
				depth++
			} else if t.Text == ")" {
				depth--
			} else if depth == 0 && t.Kind == TokKeyword &&
				(t.Lower == "select" || t.Lower == "search" || t.Lower == "cycle") {
				break
			} else if depth == 0 && t.Text == "," {
				break
			}
			i++
		}
	}
	return i
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
	asText := " as ("
	if i < len(cte) && cte[i].Kind == TokKeyword && cte[i].Lower == "as" {
		i++
		if j := skipMaterialized(cte, i); j > i {
			asText = " as " + plainJoin(cte[i:j]) + " ("
			i = j
		}
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
	lines = append(lines, strings.Repeat(" ", baseIndent)+name+asText)
	for _, l := range bodyLines {
		lines = append(lines, strings.Repeat(" ", bodyIndent)+l)
	}
	closing := strings.Repeat(" ", baseIndent) + ")"
	if tail := trimTokens(cte[close+1:]); len(tail) > 0 {
		closing += " " + plainJoin(tail)
	}
	if more {
		closing += ","
	}
	closing += trailingCommentSuffix(cte)
	lines = append(lines, closing)
	return lines
}
