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

	stmts := splitStatements(toks)
	var out []string
	for _, s := range stmts {
		out = append(out, formatStatement(s))
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.Join(out, "\n\n") + "\n", nil
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
	default:
		lines = []string{flatJoin(body)}
	}

	out := strings.Join(lines, "\n")
	if hasSemi {
		out += ";"
	}
	return out
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
