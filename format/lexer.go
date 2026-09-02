// Package format implements a gofmt-style formatter for PostgreSQL SQL.
package format

import (
	"fmt"
	"strings"
)

// TokenKind classifies a lexed token.
type TokenKind int

const (
	TokKeyword TokenKind = iota
	TokIdent
	TokString
	TokDollarString
	TokNumber
	TokOperator
	TokPunct
	TokLineComment
	TokBlockComment
	TokParam
	TokBackslashCmd
	TokEOF
)

// Token is a single lexed unit of source text.
type Token struct {
	Kind TokenKind
	Text string // original text, exact casing
	// Lower is the lowercased form, used for keyword/operator matching.
	// For non-keyword/identifier tokens it mirrors Text.
	Lower string
	Line  int
	Col   int // 0-indexed column of the first rune of Text

	// LeadingBlank is true when at least one fully blank source line
	// appeared between the previous token (or comment) and this one.
	LeadingBlank bool

	// Comments holds comment tokens attached to this token by attachComments:
	// leading comments (own line(s) before this token) precede it in this
	// slice; a single trailing same-line comment (if any) is appended last
	// and marked via TrailingOf below. Populated by a post-pass, not by Lex.
	Comments []Token
	// TrailingComment, if non-nil, is a comment on the same source line as
	// this token, after it, and should be rendered at the end of that line.
	TrailingComment *Token
}

// keywords is the curated table of SQL keywords sqlfmt recognizes. Anything
// not in this table lexes as TokIdent (a function/column/table name).
var keywords = buildKeywordSet([]string{
	"select", "from", "where", "group", "by", "having", "order",
	"insert", "into", "update", "set", "delete", "returning",
	"with", "recursive", "values", "union", "all", "intersect", "except",
	"join", "left", "right", "full", "inner", "outer", "cross", "on", "using",
	"and", "or", "not", "null", "is", "in", "between", "like", "ilike",
	"case", "when", "then", "else", "end",
	"as", "distinct", "limit", "offset", "asc", "desc", "nulls", "first", "last",
	"create", "table", "schema", "if", "exists", "alter", "drop",
	"primary", "key", "unique", "check", "references", "default",
	"begin", "commit", "rollback", "transaction",
	"over", "partition", "window", "rows", "range", "unbounded",
	// The aggregate suffixes. Without them "count(*) FILTER (WHERE ...)"
	// came out as "count(*) FILTER(where ...)" and "WITHIN GROUP" as
	// "WITHIN group" -- rule 1 lowercasing half of each construct because
	// only its second word was in this table.
	"filter", "within",
	"preceding", "following", "current", "row",
	"grouping", "sets", "cube", "rollup",
	"cast", "language", "function", "returns", "true", "false",
	"lateral", "only", "of", "to", "for",
	"conflict", "do", "nothing", "constraint", "foreign", "materialized",
	"explain",
	// DDL words, so rule 1 lowercases them consistently: without these a
	// CREATE TRIGGER came out as "create TRIGGER ... for EACH row", with
	// the words in the keyword table lowercased and the rest left as
	// typed. "cost" is deliberately omitted -- far too plausible a column
	// name to start treating as a keyword.
	"trigger", "procedure", "each", "after", "before", "execute", "returns",
	"index", "view", "statistics", "sequence", "include", "tablespace",
	"merge", "matched", "source", "target",
	"immutable", "stable", "volatile", "strict", "parallel", "security",
	"definer", "invoker", "concurrently", "unlogged", "inherits",
	// The ALTER/GRANT/CREATE DATABASE vocabulary, for the same reason:
	// without it "ALTER TABLE t ATTACH PARTITION p" came back as
	// "alter table t ATTACH partition p", and "GRANT SELECT" as
	// "GRANT select" -- rule 1 lowercasing whichever word of the pair
	// already happened to be in this table.
	"prepare", "deallocate",
	"attach", "detach", "validate", "rename", "add", "column",
	"privileges", "database", "encoding", "owner", "grant", "revoke",
	"template", "cascade", "restrict", "role", "usage", "tables",
	"sequences", "routines", "extension", "enable", "disable",
	// "partitions" (plural) and "split" complete the PG 19 partition
	// subcommands. "split" has to be a keyword because alterHeaderEnd
	// only accepts a TokKeyword as a subcommand opener, and "partitions"
	// because rule 4 drops the space before "(" after an identifier, so
	// "MERGE PARTITIONS (a, b)" came out as "merge partitions(a, b)".
	"partitions", "split",
	// SQL/PGQ (PG 19). "match" and "columns" have to be keywords for
	// splitGraphTable to find the two halves of a GRAPH_TABLE, and the
	// rest are here for rule 1: without them "CREATE PROPERTY GRAPH ...
	// VERTEX TABLES" came back with half its words lowercased and half
	// left as typed, since only "tables" was in this table.
	"graph_table", "graph", "property", "properties", "match", "columns",
	"vertex", "edge", "relationship", "node", "destination",
})

func buildKeywordSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// multiCharOperators lists operator lexemes longer than one character, tried
// longest-first at each scan position -- ordering strictly by length (not
// just alphabetically) matters: e.g. "#>>" must be tried before "#>", or
// the latter would match its first two characters and leave a stray ">"
// behind.
//
// This set is every multi-character operator name registered in
// PostgreSQL 17's pg_operator catalog (queried directly: `select distinct
// oprname from pg_operator where length(oprname) > 1`), with pg_trgm,
// hstore, intarray, cube, earthdistance, and ltree all CREATE EXTENSIONed
// first -- covering not just core arithmetic/comparison/JSON/array/range
// operators but the GiST/GIN/SP-GiST index operator classes actually used
// in this repo's own corpus (which has hstore, intarray, cube/earthdistance,
// and trigram example chapters): geometric &&/&</&>/<->/@-@/..., full-text
// @@/@@@, trigram %/<->/<->>>(word-similarity)/<<<->(strict word-similarity),
// hstore ?/?&/?|/->, ltree @>/<@/?, and the (deprecated but still
// catalogued) path/polygon "before"/"after" */< family. "!=" and "::"
// aren't in pg_operator (the parser normalizes "!=" to "<>", and "::" is
// cast syntax, not a real operator) but are still real input syntax, so
// are kept too.
var multiCharOperators = []string{
	// 5-character
	"<->>>", "<<<->",
	// 4-character
	"!~~*", "#<=#", "#>=#", "<->>", "<<->", "~<=~", "~>=~",
	// 3-character
	"!~*", "!~~", "#<#", "#>#", "#>>", "%>>", "&<|", "*<=", "*<>", "*>=",
	"->>", "-|-", "<#>", "<->", "<<%", "<<=", "<<|", "<=>", "<@>", ">>=",
	"?-|", "?<@", "?@>", "?||", "@-@", "@@@", "^<@", "^@>", "|&>", "|>>",
	"||/", "~<~", "~>~", "~~*",
	// 2-character
	"!!", "!=", "!~", "##", "#-", "#=", "#>", "%#", "%%", "%>", "&&", "&<",
	"&>", "*<", "*=", "*>", "::", "->", "<%", "<<", "<=", "<>", "<@", "<^",
	">=", ">>", ">^", "?#", "?&", "?-", "?@", "?|", "?~", "@>", "@?", "@@",
	"^?", "^@", "^~", "|/", "||", "~*", "~=", "~>", "~~",
}

type lexer struct {
	src  []byte
	pos  int
	line int
	col  int
}

// Lex tokenizes src into a flat token stream, including comments.
func Lex(src []byte) ([]Token, error) {
	l := &lexer{src: src, line: 1, col: 0}
	var toks []Token
	blankPending := false

	for {
		blank := l.skipSpaceTrackingBlankLines()
		if blank {
			blankPending = true
		}
		if l.pos >= len(l.src) {
			break
		}

		startLine, startCol := l.line, l.col
		tok, err := l.scanToken()
		if err != nil {
			return nil, err
		}
		tok.Line, tok.Col = startLine, startCol
		tok.LeadingBlank = blankPending
		blankPending = false
		toks = append(toks, tok)
	}

	toks = append(toks, Token{Kind: TokEOF, Line: l.line, Col: l.col})
	return toks, nil
}

func (l *lexer) peekByte() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekAt(off int) byte {
	if l.pos+off >= len(l.src) {
		return 0
	}
	return l.src[l.pos+off]
}

func (l *lexer) advance() byte {
	b := l.src[l.pos]
	l.pos++
	if b == '\n' {
		l.line++
		l.col = 0
	} else {
		l.col++
	}
	return b
}

// skipSpaceTrackingBlankLines consumes whitespace (not comments) and
// reports whether a fully blank source line was skipped.
func (l *lexer) skipSpaceTrackingBlankLines() bool {
	newlines := 0
	for l.pos < len(l.src) {
		b := l.peekByte()
		if b == ' ' || b == '\t' || b == '\r' {
			l.advance()
			continue
		}
		if b == '\n' {
			newlines++
			l.advance()
			continue
		}
		break
	}
	return newlines >= 2
}

func (l *lexer) scanToken() (Token, error) {
	b := l.peekByte()

	switch {
	case b == '\'':
		return l.scanString()
	case isStringPrefix(b) && l.peekAt(1) == '\'':
		// E'...', B'...', X'...' and U&'...': the prefix is part of the
		// literal and must stay glued to the opening quote. Lexed as a
		// bare identifier followed by a string, spaceBetween put a space
		// between them -- and "E '\n'" is not valid PostgreSQL at all
		// ("type \"e\" does not exist"), so the formatter was emitting SQL
		// that does not parse.
		return l.scanPrefixedString()
	case (b == 'u' || b == 'U') && l.peekAt(1) == '&' && l.peekAt(2) == '\'':
		return l.scanPrefixedString()
	case b == '"':
		return l.scanQuotedIdent()
	case b == '$':
		if isDollarQuoteStart(l.src, l.pos) {
			return l.scanDollarString()
		}
		return l.scanParam()
	case b == '-' && l.peekAt(1) == '-':
		return l.scanLineComment()
	case b == '/' && l.peekAt(1) == '*':
		return l.scanBlockComment()
	case b == '\\':
		return l.scanBackslashCmd()
	case b == ':' && l.peekAt(1) != ':':
		return l.scanParam()
	case isDigit(b):
		return l.scanNumber()
	case isIdentStart(b):
		return l.scanIdentOrKeyword()
	case isPunct(b):
		start := l.pos
		l.advance()
		text := string(l.src[start:l.pos])
		return Token{Kind: TokPunct, Text: text, Lower: text}, nil
	default:
		return l.scanOperator()
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' }

// isHigh reports whether b has its high bit set, i.e. is one byte of a
// multi-byte UTF-8 sequence. PostgreSQL puts that whole range in its
// identifier character classes -- scan.l has
//
//	ident_start	[A-Za-z\200-\377_]
//	ident_cont	[A-Za-z\200-\377_0-9\$]
//
// and the same for dollar-quote tags (dolq_start/dolq_cont) -- so an
// accented identifier like "prenom" spelled with an e-acute is one token,
// not one token per byte. Scanning those bytes individually turned
// "prenom" into "pr <byte> <byte> nom", which is data loss in the plainest
// sense: the output is no longer the input.
func isHigh(b byte) bool { return b >= 0x80 }

func isIdentStart(b byte) bool { return isAlpha(b) || isHigh(b) }
func isIdentCont(b byte) bool  { return isAlpha(b) || isDigit(b) || isHigh(b) || b == '$' }
func isPunct(b byte) bool {
	switch b {
	case '(', ')', '[', ']', ',', ';':
		return true
	}
	return false
}

// isStringPrefix reports whether b introduces a prefixed string literal.
func isStringPrefix(b byte) bool {
	switch b {
	case 'e', 'E', 'b', 'B', 'x', 'X':
		return true
	}
	return false
}

// scanPrefixedString scans E'...', B'...', X'...' or U&'...' as one token,
// prefix included.
func (l *lexer) scanPrefixedString() (Token, error) {
	start := l.pos
	l.advance() // the prefix letter
	if l.peekByte() == '&' {
		l.advance()
	}
	str, err := l.scanString()
	if err != nil {
		return Token{}, err
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: str.Kind, Text: text, Lower: text}, nil
}

func (l *lexer) scanString() (Token, error) {
	startLine := l.line
	var sb strings.Builder
	sb.WriteByte(l.advance()) // opening '
	for {
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("sqlfmt: unterminated string literal starting at line %d", startLine)
		}
		b := l.peekByte()
		if b == '\'' {
			sb.WriteByte(l.advance())
			if l.peekByte() == '\'' {
				sb.WriteByte(l.advance())
				continue
			}
			break
		}
		sb.WriteByte(l.advance())
	}
	text := sb.String()
	return Token{Kind: TokString, Text: text, Lower: text}, nil
}

func (l *lexer) scanQuotedIdent() (Token, error) {
	startLine := l.line
	var sb strings.Builder
	sb.WriteByte(l.advance()) // opening "
	for {
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("sqlfmt: unterminated quoted identifier starting at line %d", startLine)
		}
		b := l.peekByte()
		if b == '"' {
			sb.WriteByte(l.advance())
			if l.peekByte() == '"' {
				sb.WriteByte(l.advance())
				continue
			}
			break
		}
		sb.WriteByte(l.advance())
	}
	text := sb.String()
	return Token{Kind: TokIdent, Text: text, Lower: text}, nil
}

// isDollarQuoteStart reports whether pos begins a dollar-quote opening
// delimiter: $tag$ where tag is an (possibly empty) identifier.
func isDollarQuoteStart(src []byte, pos int) bool {
	if pos >= len(src) || src[pos] != '$' {
		return false
	}
	i := pos + 1
	for i < len(src) && isIdentCont(src[i]) && src[i] != '$' {
		i++
	}
	return i < len(src) && src[i] == '$'
}

func (l *lexer) scanDollarString() (Token, error) {
	startLine := l.line
	start := l.pos
	l.advance() // $
	for isIdentCont(l.peekByte()) && l.peekByte() != '$' {
		l.advance()
	}
	l.advance() // closing $ of the opening delimiter
	delim := string(l.src[start:l.pos])

	for {
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("sqlfmt: unterminated dollar-quoted string starting at line %d", startLine)
		}
		if l.peekByte() == '$' && l.pos+len(delim) <= len(l.src) && string(l.src[l.pos:l.pos+len(delim)]) == delim {
			for range delim {
				l.advance()
			}
			break
		}
		l.advance()
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokDollarString, Text: text, Lower: text}, nil
}

// SplitDollarQuoted splits a dollar-quoted string literal into its
// delimiter and its body: "$tag$body$tag$" -> ("$tag$", "body", true). It
// reports false for anything that is not exactly one complete dollar-quoted
// string.
//
// The delimiter rules are the lexer's own (isDollarQuoteStart /
// scanDollarString), which are PostgreSQL's: an opening "$", an optional
// identifier tag, a closing "$", and a body that runs to the next
// occurrence of that same delimiter. Both body formatters -- LANGUAGE SQL
// and PL/pgSQL -- go through this rather than hand-parsing, because
// PostgreSQL uses one lexer for dollar quoting regardless of the language
// inside, and a formatter that guessed differently in either place would
// mis-split a body whose text contains a "$".
func SplitDollarQuoted(s string) (delim, body string, ok bool) {
	if !isDollarQuoteStart([]byte(s), 0) {
		return "", "", false
	}
	i := 1
	for i < len(s) && isIdentCont(s[i]) && s[i] != '$' {
		i++
	}
	if i >= len(s) || s[i] != '$' {
		return "", "", false
	}
	delim = s[:i+1]
	rest := s[len(delim):]
	end := strings.Index(rest, delim)
	if end < 0 {
		return "", "", false
	}
	// Exactly one dollar-quoted string: nothing may follow the closing
	// delimiter, or this is a body with trailing options we must not touch.
	if len(rest) != end+len(delim) {
		return "", "", false
	}
	return delim, rest[:end], true
}

func (l *lexer) scanParam() (Token, error) {
	start := l.pos
	l.advance() // : or $
	for isIdentCont(l.peekByte()) {
		l.advance()
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokParam, Text: text, Lower: strings.ToLower(text)}, nil
}

func (l *lexer) scanLineComment() (Token, error) {
	start := l.pos
	l.advance()
	l.advance()
	for l.pos < len(l.src) && l.peekByte() != '\n' {
		l.advance()
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokLineComment, Text: text, Lower: text}, nil
}

func (l *lexer) scanBlockComment() (Token, error) {
	startLine := l.line
	start := l.pos
	l.advance()
	l.advance()
	depth := 1
	for depth > 0 {
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("sqlfmt: unterminated block comment starting at line %d", startLine)
		}
		if l.peekByte() == '/' && l.peekAt(1) == '*' {
			l.advance()
			l.advance()
			depth++
			continue
		}
		if l.peekByte() == '*' && l.peekAt(1) == '/' {
			l.advance()
			l.advance()
			depth--
			continue
		}
		l.advance()
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokBlockComment, Text: text, Lower: text}, nil
}

// scanBackslashCmd consumes a psql meta-command line (e.g. \set x y) as one
// opaque token so the lexer tolerates psql transcript fragments without
// attempting to understand them.
func (l *lexer) scanBackslashCmd() (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && l.peekByte() != '\n' {
		// psql \set values may themselves contain quoted strings with
		// embedded content; since this token is opaque pass-through we
		// simply scan to end of line.
		l.advance()
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokBackslashCmd, Text: text, Lower: text}, nil
}

func (l *lexer) scanNumber() (Token, error) {
	start := l.pos
	for isDigit(l.peekByte()) {
		l.advance()
	}
	if l.peekByte() == '.' && isDigit(l.peekAt(1)) {
		l.advance()
		for isDigit(l.peekByte()) {
			l.advance()
		}
	}
	if l.peekByte() == 'e' || l.peekByte() == 'E' {
		save := l.pos
		saveLine, saveCol := l.line, l.col
		l.advance()
		if l.peekByte() == '+' || l.peekByte() == '-' {
			l.advance()
		}
		if isDigit(l.peekByte()) {
			for isDigit(l.peekByte()) {
				l.advance()
			}
		} else {
			l.pos = save
			l.line, l.col = saveLine, saveCol
		}
	}
	text := string(l.src[start:l.pos])
	return Token{Kind: TokNumber, Text: text, Lower: text}, nil
}

func (l *lexer) scanIdentOrKeyword() (Token, error) {
	start := l.pos
	for isIdentCont(l.peekByte()) {
		l.advance()
	}
	text := string(l.src[start:l.pos])
	lower := strings.ToLower(text)
	if keywords[lower] {
		return Token{Kind: TokKeyword, Text: text, Lower: lower}, nil
	}
	return Token{Kind: TokIdent, Text: text, Lower: lower}, nil
}

func (l *lexer) scanOperator() (Token, error) {
	for _, op := range multiCharOperators {
		n := len(op)
		if l.pos+n <= len(l.src) && string(l.src[l.pos:l.pos+n]) == op {
			for i := 0; i < n; i++ {
				l.advance()
			}
			return Token{Kind: TokOperator, Text: op, Lower: op}, nil
		}
	}
	b := l.advance()
	text := string(b)
	return Token{Kind: TokOperator, Text: text, Lower: text}, nil
}
