package format

import (
	"fmt"
	"io"
	"strings"
)

// Format reads SQL from r and returns it reformatted per the house style
// documented in STYLE.md.
func Format(r io.Reader) (string, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	toks, err := Lex(src)
	if err != nil {
		return "", fmt.Errorf("sqlfmt: %w", err)
	}
	toks, eofComments := attachComments(toks)

	stmts := splitStatements(toks)
	var out []string
	for _, s := range stmts {
		out = append(out, formatStatement(s))
	}
	if trailing := renderLeadingComments(eofComments, 0); len(trailing) > 0 {
		out = append(out, strings.Join(trailing, "\n"))
	}
	if len(out) == 0 {
		return "", nil
	}
	result := strings.Join(out, "\n\n") + "\n"
	return alignTrailingComments(result), nil
}

// splitStatements splits the token stream on top-level ";" tokens. Each
// returned slice includes its trailing ";" (if any) but excludes the
// terminal EOF token.
func splitStatements(toks []Token) [][]Token {
	var stmts [][]Token
	depth := 0
	start := 0
	for i, t := range toks {
		if t.Kind == TokEOF {
			break
		}
		if t.Text == "(" {
			depth++
		} else if t.Text == ")" {
			depth--
		} else if depth == 0 && t.Text == ";" {
			stmts = append(stmts, toks[start:i+1])
			start = i + 1
		}
	}
	if start < len(toks) {
		tail := trimTokens(toks[start:])
		// drop the trailing EOF sentinel if it slipped through trimTokens
		var real []Token
		for _, t := range tail {
			if t.Kind != TokEOF {
				real = append(real, t)
			}
		}
		if len(real) > 0 {
			stmts = append(stmts, real)
		}
	}
	return stmts
}

// statementKeyword returns the lowercase leading keyword(s) that determine
// how a statement should be dispatched.
func statementKeyword(toks []Token) string {
	toks = trimTokens(toks)
	if len(toks) == 0 {
		return ""
	}
	if toks[0].Kind == TokBackslashCmd {
		return "\\"
	}
	if toks[0].Kind != TokKeyword {
		return ""
	}
	first := toks[0].Lower
	if first == "create" || first == "drop" || first == "alter" {
		// Skip the optional "or replace" / "unique" / "materialized" /
		// "if not exists" noise between the verb and the object kind.
		for i := 1; i < len(toks) && i < 6; i++ {
			if toks[i].Kind != TokKeyword && toks[i].Kind != TokIdent {
				break
			}
			switch toks[i].Lower {
			case "or", "replace", "unique", "if", "not", "exists", "recursive",
				"temp", "temporary", "unlogged", "concurrently":
				continue
			case "table", "index", "function", "view", "trigger", "statistics",
				"sequence", "procedure", "materialized":
				kind := toks[i].Lower
				if kind == "materialized" {
					// "create materialized view" lays out as a view.
					kind = "view"
				}
				if first == "create" {
					return "create " + kind
				}
				return first
			default:
				return first
			}
		}
	}
	return first
}

func formatStatement(toks []Token) string {
	toks = trimTokens(toks)
	if len(toks) == 0 {
		return ""
	}

	var leading []string
	if len(toks[0].Comments) > 0 {
		leading = renderLeadingComments(toks[0].Comments, 0)
		toks[0].Comments = nil
	}
	if len(leading) > 0 {
		return strings.Join(leading, "\n") + "\n" + formatStatement(toks)
	}

	// A leading psql meta-command (e.g. "\copy ... from ... with csv") is
	// only ever a prefix line, never the whole statement -- whatever
	// tokens follow it are a real SQL statement in their own right and
	// must still be formatted, not discarded.
	if toks[0].Kind == TokBackslashCmd {
		rest := trimTokens(toks[1:])
		if len(rest) == 0 {
			return toks[0].Text
		}
		return toks[0].Text + "\n\n" + formatStatement(rest)
	}

	hasSemi := toks[len(toks)-1].Text == ";"
	body := toks
	if hasSemi {
		body = trimTokens(toks[:len(toks)-1])
	}

	kw := statementKeyword(toks)
	var lines []string
	switch kw {
	case "begin", "commit", "rollback":
		lines = []string{flatJoin(body)}
	case "create table":
		lines = layoutCreateTable(body)
	case "create index", "create function", "create view", "create trigger",
		"create statistics", "create sequence", "create procedure":
		lines = layoutDDL(body)
	case "select", "insert", "update", "delete", "with":
		lines = formatQuerySegment(body, 0)
	case "explain":
		lines = layoutExplain(body)
	case "":
		// A statement that opens with "(" -- the parenthesized arms of a
		// set operation -- is still a query, and formatQuerySegment knows
		// how to unwrap it. Anything else with no leading keyword falls
		// through to the flat default below.
		if len(body) > 0 && body[0].Text == "(" {
			lines = formatQuerySegment(body, 0)
		} else {
			lines = []string{flatJoin(body)}
		}
	default:
		lines = []string{flatJoin(body)}
	}

	out := strings.Join(lines, "\n")
	if hasSemi {
		out += ";"
		if tc := toks[len(toks)-1].TrailingComment; tc != nil {
			out += commentMarker + trailingCommentText(tc)
		}
	} else if tc := toks[len(toks)-1].TrailingComment; tc != nil {
		out += commentMarker + trailingCommentText(tc)
	}
	return out
}

// ddlClauseWords are the keywords that introduce a continuation clause in
// the DDL statements layoutDDL handles. Order matters only for the two-word
// entries, which must precede any single-word entry sharing their first
// word.
var ddlClauseWords = []struct {
	words []string
	name  string
}{
	{[]string{"security", "definer"}, "security definer"},
	{[]string{"security", "invoker"}, "security invoker"},
	{[]string{"returns"}, "returns"},
	{[]string{"language"}, "language"},
	{[]string{"using"}, "using"},
	{[]string{"include"}, "include"},
	{[]string{"tablespace"}, "tablespace"},
	{[]string{"where"}, "where"},
	{[]string{"from"}, "from"},
	{[]string{"on"}, "on"},
	{[]string{"as"}, "as"},
	{[]string{"immutable"}, "immutable"},
	{[]string{"stable"}, "stable"},
	{[]string{"volatile"}, "volatile"},
	{[]string{"strict"}, "strict"},
	{[]string{"parallel"}, "parallel"},
	{[]string{"cost"}, "cost"},
	{[]string{"execute"}, "execute"},
	{[]string{"for"}, "for"},
	{[]string{"before"}, "before"},
	{[]string{"after"}, "after"},
}

// layoutDDL renders CREATE FUNCTION / INDEX / VIEW / TRIGGER / STATISTICS
// and friends. Everything except CREATE TABLE used to fall through
// formatStatement's default arm to flatJoin, which put the whole header on
// one line -- a hand-written four-line CREATE FUNCTION header came back at
// 138 columns, and 47 CREATE INDEX statements lost their line breaks.
//
// The shape is the one the rest of the tool uses: the object being created
// stays on the first line, and each continuation clause goes on its own
// line, right-padded so the clause keywords end at a common column -- a
// river, computed over the continuation clauses only. The "create ..."
// line is deliberately not part of that river: it carries the object name
// and argument list and is routinely 40+ characters, which would push every
// other line off the page.
func layoutDDL(toks []Token) []string {
	bounds := ddlClauseBounds(toks)
	if len(bounds) == 0 {
		return []string{flatJoin(toks)}
	}
	head := flatJoin(toks[:bounds[0].idx])
	if len(head)+1+len(flatJoin(toks[bounds[0].idx:])) <= targetWidth {
		return []string{flatJoin(toks)}
	}

	width := 0
	for _, b := range bounds {
		if len(b.name) > width {
			width = len(b.name)
		}
	}

	// Resolve each clause's body span first: hoistLanguage reorders the
	// clauses, and a clause's span is defined by where the *next* one
	// starts in the source, not in the output.
	clauses := make([]ddlClause, 0, len(bounds))
	for bi, b := range bounds {
		end := len(toks)
		if bi+1 < len(bounds) {
			end = bounds[bi+1].idx
		}
		clauses = append(clauses, ddlClause{b.name, flatJoin(toks[b.idx+len(b.words) : end])})
	}
	clauses = hoistLanguage(clauses)
	clauses = formatSQLBody(clauses)

	lines := []string{head}
	for _, c := range clauses {
		line := strings.Repeat(" ", width-len(c.name)) + c.name
		if c.body != "" {
			line += " " + c.body
		}
		lines = append(lines, line)
	}
	return lines
}

// formatSQLBody reformats a LANGUAGE SQL function body. The body of such a
// function is SQL, and this package formats SQL, so there is no reason to
// leave it as whatever the author typed while every other statement in the
// file gets laid out.
//
// LANGUAGE SQL bodies go through Format; LANGUAGE PLPGSQL bodies go
// through FormatPlpgsql. Every other language -- plpython, plperl, plv8 --
// is passed through verbatim, which is the only safe answer: the lexer
// treats a dollar-quoted body as a single opaque token, so a body this
// package does not understand is preserved exactly as written.
func formatSQLBody(clauses []ddlClause) []ddlClause {
	lang := ""
	for _, c := range clauses {
		if c.name == "language" {
			lang = strings.ToLower(strings.TrimSpace(c.body))
		}
	}
	if lang != "sql" && lang != "plpgsql" {
		return clauses
	}
	out := make([]ddlClause, len(clauses))
	copy(out, clauses)
	for i, c := range out {
		if c.name != "as" {
			continue
		}
		tag, inner, ok := SplitDollarQuoted(strings.TrimSpace(c.body))
		if !ok {
			continue
		}
		var formatted string
		if lang == "plpgsql" {
			pl, pok := FormatPlpgsql(inner, 0)
			if !pok {
				continue // not a skeleton we recognise: leave it alone
			}
			// Only rewrite when it is an improvement. A PL/pgSQL body is
			// mostly embedded SQL, and a construct the SQL layout handles
			// poorly -- a SET with several CASE expressions, say -- can
			// come back wider and more ragged than the author's own
			// version. The author's version is then the better answer, and
			// leaving it is always correct.
			// Indentation legitimately lengthens lines, so the test is
			// not "longer than before" but "pushed past the target width
			// when it was not there already".
			if maxLineLen(pl) > targetWidth && maxLineLen(pl) > maxLineLen(inner) {
				continue
			}
			formatted = pl
		} else {
			f, err := Format(strings.NewReader(inner))
			if err != nil {
				continue // unparseable body: leave it exactly as written
			}
			formatted = f
		}
		out[i].body = tag + "\n" + strings.TrimRight(formatted, "\n") + "\n" + tag
	}
	return out
}

// maxLineLen returns the length of the longest line in s.
func maxLineLen(s string) int {
	m := 0
	for _, l := range strings.Split(s, "\n") {
		if len(l) > m {
			m = len(l)
		}
	}
	return m
}

// ddlClause is one rendered continuation clause: its keyword and the text
// that follows it.
type ddlClause struct{ name, body string }

// hoistLanguage moves a LANGUAGE clause that trails the body back into the
// header, ahead of AS. PostgreSQL accepts a function's option list in any
// order, so "as $$ ... $$ language plpgsql" is legal and common -- but it
// buries the single most useful fact about a function, what language it is
// written in, behind however many hundred lines its body runs to. Reading
// the declaration should not require scrolling past the implementation.
// Reordering options changes nothing about what the statement does.
func hoistLanguage(clauses []ddlClause) []ddlClause {
	asAt, langAt := -1, -1
	for i, c := range clauses {
		switch c.name {
		case "as":
			if asAt == -1 {
				asAt = i
			}
		case "language":
			langAt = i
		}
	}
	if asAt == -1 || langAt == -1 || langAt < asAt {
		return clauses
	}
	out := make([]ddlClause, 0, len(clauses))
	out = append(out, clauses[:asAt]...)
	out = append(out, clauses[langAt])
	for i := asAt; i < len(clauses); i++ {
		if i != langAt {
			out = append(out, clauses[i])
		}
	}
	return out
}

// matchDDLWords is matchWords without the TokKeyword requirement. Several
// DDL clause introducers -- BEFORE, AFTER, EXECUTE, RETURNS, INCLUDE,
// TABLESPACE -- are not in the lexer's keyword table and lex as plain
// identifiers, but in this position they are unambiguously clause
// keywords.
func matchDDLWords(toks []Token, i int, words []string) bool {
	for j, w := range words {
		if i+j >= len(toks) || toks[i+j].Lower != w {
			return false
		}
	}
	return true
}

type ddlBound struct {
	idx   int
	name  string
	words []string
}

// ddlClauseBounds finds each top-level DDL continuation clause. A clause
// keyword inside parentheses (a column list, an argument list, an index
// expression) belongs to that construct, not to the statement.
func ddlClauseBounds(toks []Token) []ddlBound {
	var out []ddlBound
	depth := 0
	for i := 0; i < len(toks); i++ {
		switch toks[i].Text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		for _, cw := range ddlClauseWords {
			if matchDDLWords(toks, i, cw.words) {
				out = append(out, ddlBound{idx: i, name: cw.name, words: cw.words})
				i += len(cw.words) - 1
				break
			}
		}
	}
	return out
}

// layoutExplain renders EXPLAIN per STYLE.md rule 19: the EXPLAIN prefix
// (with its option list, if any) takes a line of its own, and is padded
// into the same clause river as the statement it wraps -- "explain" is a
// clause keyword like "select" or "order by", not a thing bolted on above
// them:
//
//	explain (analyze, buffers)
//	 select res.raceid, res.points
//	   from f1db.results res
//	  where res.points >= 10;
//
// That falls out of clauseWords listing "explain": splitClauses yields it
// as an ordinary segment whose body is the option list, riverWidth sizes
// the river with it included, and renderClause pads it. So a query whose
// longest clause keyword is wider than "explain" indents the EXPLAIN line
// instead, exactly as it would any other short clause keyword.
//
// Both spellings of the prefix are handled: the modern parenthesized option
// list ("explain (analyze, buffers) select ...") and the legacy bare form
// the grammar still accepts ("explain analyze verbose select ..."). ANALYZE
// and VERBOSE are the only two words the legacy form allows, so consuming
// exactly those is precise rather than a guess about where the inner
// statement starts.
//
// A payload that is not a clause-river query -- "explain execute p(1)", or
// anything starting with WITH, whose CTE list has a layout of its own that
// no single river spans -- can't participate in a river, so it falls back
// to an unpadded prefix line plus that statement formatted as it would be
// on its own.
func layoutExplain(toks []Token) []string {
	prefix, rest := splitExplainPrefix(toks)

	// "explain" with nothing after it is not a statement we can improve on.
	if len(rest) == 0 {
		return []string{plainJoin(prefix)}
	}

	if explainPayloadJoinsRiver(rest) {
		return formatQuerySegment(toks, 0)
	}

	// plainJoin would render "explain(analyze)" -- spaceBetween treats "("
	// after a name as a call, per STYLE.md rule 4. The option list is not a
	// call, so the space goes in here.
	head := prefix[0].Text
	if len(prefix) > 1 {
		head += " " + plainJoin(prefix[1:])
	}
	return append([]string{head}, strings.Split(formatStatement(rest), "\n")...)
}

// splitExplainPrefix splits "explain [ (opts) | analyze verbose ]" off the
// front of toks, returning the prefix tokens and the statement they wrap.
func splitExplainPrefix(toks []Token) (prefix, rest []Token) {
	prefix = []Token{toks[0]}
	i := 1

	if i < len(toks) && toks[i].Text == "(" {
		depth := 0
		for ; i < len(toks); i++ {
			if toks[i].Text == "(" {
				depth++
			} else if toks[i].Text == ")" {
				depth--
				if depth == 0 {
					prefix = append(prefix, toks[i])
					i++
					break
				}
			}
			if depth > 0 {
				prefix = append(prefix, toks[i])
			}
		}
	} else {
		for i < len(toks) && (toks[i].Lower == "analyze" || toks[i].Lower == "verbose") {
			prefix = append(prefix, toks[i])
			i++
		}
	}
	return prefix, trimTokens(toks[i:])
}

// explainPayloadJoinsRiver reports whether the statement an EXPLAIN wraps is
// one whose clauses form a single river the EXPLAIN line can be padded into.
// WITH is excluded deliberately: formatQuerySegment gives a CTE list its own
// per-CTE layout, with no top-level river to join.
func explainPayloadJoinsRiver(rest []Token) bool {
	if len(rest) == 0 || rest[0].Kind != TokKeyword {
		return false
	}
	switch rest[0].Lower {
	case "select", "insert", "update", "delete", "values", "table":
		return true
	}
	return false
}

// layoutCreateTable renders "create table name ( col ..., ... );" per
// STYLE.md rule 16: table name on the CREATE TABLE line, opening "(" on its
// own line indented 1 space, columns indented 2 spaces with column names
// left-padded to a common width so data types start in the same column,
// table-level constraints separated by a blank line, closing ")" at the
// opening "("'s indent.
// createTableColumnList returns the index of the "(" opening a CREATE
// TABLE's column list, or -1 when the statement has none. The column list
// is the first "(" that follows the table name directly; anything with a
// keyword in between ("partition of t for values from (", "as select ... (")
// is some other construct's paren, and treating it as the column list
// mangled the statement and dropped everything after it.
func createTableColumnList(toks []Token) int {
	for i, t := range toks {
		if t.Text != "(" {
			continue
		}
		for j := 2; j < i; j++ {
			if toks[j].Kind == TokKeyword && toks[j].Lower != "if" &&
				toks[j].Lower != "not" && toks[j].Lower != "exists" {
				return -1
			}
		}
		return i
	}
	return -1
}

// createTableAsBody returns the index of the statement a CREATE TABLE ... AS
// wraps, or -1 if this is not a CTAS. The wrapped statement is then
// formatted as it would be on its own.
func createTableAsBody(toks []Token) int {
	for i, t := range toks {
		if t.Kind == TokKeyword && t.Lower == "as" && i+1 < len(toks) {
			return i + 1
		}
	}
	return -1
}

func layoutCreateTable(toks []Token) []string {
	open := createTableColumnList(toks)
	if open == -1 {
		if body := createTableAsBody(toks); body != -1 {
			head := flatJoin(toks[:body])
			return append([]string{head}, strings.Split(formatStatement(toks[body:]), "\n")...)
		}
		return []string{flatJoin(toks)}
	}
	header := flatJoin(toks[:open])
	close := matchParen(toks, open)
	items := splitTopLevelComma(trimTokens(toks[open+1 : close]))

	constraintKws := map[string]bool{"primary": true, "unique": true, "check": true, "constraint": true, "foreign": true}
	isConstraint := func(it []Token) bool {
		it = trimTokens(it)
		return len(it) > 0 && it[0].Kind == TokKeyword && constraintKws[it[0].Lower]
	}

	maxName := 0
	for _, it := range items {
		it = trimTokens(it)
		if len(it) == 0 || isConstraint(it) {
			continue
		}
		if len(it[0].Text) > maxName {
			maxName = len(it[0].Text)
		}
	}

	lines := []string{header, " ("}
	prevWasColumn := false
	for idx, it := range items {
		it = trimTokens(it)
		if len(it) == 0 {
			continue
		}
		comma := ","
		if idx == len(items)-1 {
			comma = ""
		}
		if isConstraint(it) {
			if prevWasColumn {
				lines = append(lines, "")
			}
			lines = append(lines, "  "+flatJoin(it)+comma)
			prevWasColumn = false
			continue
		}
		name := it[0].Text
		rest := renderRun(trimTokens(it[1:]), 2+maxName+1)
		pad := strings.Repeat(" ", maxName-len(name))
		lines = append(lines, "  "+name+pad+" "+rest[0]+comma)
		lines = append(lines, rest[1:]...)
		prevWasColumn = true
	}
	// Whatever follows the column list belongs to the statement and must
	// not be dropped: PARTITION BY, INHERITS, TABLESPACE, WITH (...), the
	// lot. Losing it silently turned a partitioned table into a plain one.
	closing := ")"
	if tail := trimTokens(toks[close+1:]); len(tail) > 0 {
		closing += " " + flatJoin(tail)
	}
	lines = append(lines, closing)
	return lines
}
