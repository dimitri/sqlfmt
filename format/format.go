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
	if first == "create" && len(toks) > 1 && toks[1].Kind == TokKeyword && toks[1].Lower == "table" {
		return "create table"
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
	case "select", "insert", "update", "delete", "with":
		lines = formatQuerySegment(body, 0)
	case "explain":
		lines = layoutExplain(body)
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

// layoutExplain renders EXPLAIN per STYLE.md rule 19: the EXPLAIN prefix
// (with its option list, if any) alone on the first line, then the
// statement it wraps formatted exactly as if it had been written on its
// own -- its own clause river, unindented. That is what the corpus does in
// 185 of 192 cases.
//
// Both spellings of the prefix are handled: the modern parenthesized option
// list ("explain (analyze, buffers) select ...") and the legacy bare form
// the grammar still accepts ("explain analyze verbose select ..."). ANALYZE
// and VERBOSE are the only two words the legacy form allows, so consuming
// exactly those is precise rather than a guess about where the inner
// statement starts.
//
// The wrapped statement is dispatched back through formatStatement, which
// is what makes "explain (analyze) execute p(1)" and "explain select ..."
// each come out formatted the way that statement would be on its own.
func layoutExplain(toks []Token) []string {
	prefix := []Token{toks[0]}
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

	// "explain" with nothing after it is not a statement we can improve on.
	rest := trimTokens(toks[i:])
	if len(rest) == 0 {
		return []string{plainJoin(prefix)}
	}

	// plainJoin would render "explain(analyze)" -- spaceBetween treats "("
	// after a name as a call, per STYLE.md rule 4. The option list is not a
	// call, so the space goes in here.
	head := prefix[0].Text
	if len(prefix) > 1 {
		head += " " + plainJoin(prefix[1:])
	}

	lines := []string{head}
	return append(lines, strings.Split(formatStatement(rest), "\n")...)
}

// layoutCreateTable renders "create table name ( col ..., ... );" per
// STYLE.md rule 16: table name on the CREATE TABLE line, opening "(" on its
// own line indented 1 space, columns indented 2 spaces with column names
// left-padded to a common width so data types start in the same column,
// table-level constraints separated by a blank line, closing ")" at the
// opening "("'s indent.
func layoutCreateTable(toks []Token) []string {
	open := -1
	for i, t := range toks {
		if t.Text == "(" {
			open = i
			break
		}
	}
	if open == -1 {
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
	lines = append(lines, ")")
	return lines
}
