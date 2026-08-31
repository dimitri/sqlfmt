package format

import (
	"strings"
	"testing"
)

// PostgreSQL's lexer puts the whole high-byte range in its identifier
// classes (scan.l: ident_start [A-Za-z\200-\377_]), so an accented
// identifier is one token. Scanning it byte by byte spelled it back out
// with a space between every byte.
func TestNonASCIIIdentifierSurvives(t *testing.T) {
	for _, src := range []string{
		"select prénom, âge from personne where prénom = 'Zoé';\n",
		"select \"Ünïcode\".hôtel from \"Ünïcode\";\n",
		"create table café (naïveté int);\n",
	} {
		got := mustFormat(t, src)
		for _, want := range strings.Fields(strings.Map(func(r rune) rune {
			if r == ',' || r == ';' || r == '(' || r == ')' {
				return ' '
			}
			return r
		}, src)) {
			if !strings.Contains(got, want) {
				t.Errorf("Format(%q) lost %q:\n%s", src, want, got)
			}
		}
	}
}

// A "--" comment cannot share a line with the tokens after it: whatever
// follows would be inside the comment. renderRun breaks the line for that
// reason, but flatJoin used to join those lines back with a space.
func TestLineCommentNeverSwallowsCode(t *testing.T) {
	cases := []struct{ name, src string }{
		{"select list", "select a, -- pick a\n       b\n  from t;\n"},
		{"after comma", "select aaaa, -- why\n       bbbb\n  from t;\n"},
		{"ddl", "create sequence s -- counter\n  start 10;\n"},
		{"index paren", "create index on t (a) -- speed\n where b is not null;\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, line := range strings.Split(got, "\n") {
				i := strings.Index(line, "--")
				if i < 0 {
					continue
				}
				// Everything past the marker is comment text; no SQL may
				// hide there. The tokens we care about are alphanumeric
				// runs that appear in the source outside the comment.
				if rest := line[i:]; strings.Contains(rest, ";") {
					t.Errorf("statement terminator inside a comment: %q\n%s", line, got)
				}
			}
			if !strings.Contains(got, "--") {
				t.Errorf("comment dropped entirely:\n%s", got)
			}
		})
	}
}

// A comment trailing on a separating comma belongs to no item, so the
// item-boundary check never saw it and splitTopLevelComma dropped the
// comma -- and the comment with it.
func TestCommentOnSeparatorSurvives(t *testing.T) {
	src := "select aaaa, -- first\n       bbbb, -- second\n       cccc\n  from t;\n"
	got := mustFormat(t, src)
	for _, want := range []string{"-- first", "-- second"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
}

// The comma separating two column definitions is part of the statement, so
// it has to be emitted before any trailing comment. Appending it after put
// the comma inside the comment, which is a syntax error.
func TestCreateTableCommaPrecedesComment(t *testing.T) {
	src := "create table t (\n  id bigserial primary key, -- the key\n  name text not null, -- who\n  ts timestamptz\n);\n"
	got := mustFormat(t, src)
	for _, line := range strings.Split(got, "\n") {
		i := strings.Index(line, "--")
		if i >= 0 && strings.Contains(line[i:], ",") {
			t.Errorf("comma swallowed by comment: %q\n%s", line, got)
		}
	}
	if !strings.Contains(got, "primary key,") || !strings.Contains(got, "not null,") {
		t.Errorf("column comma missing:\n%s", got)
	}
}

// Block comments nest in PostgreSQL. Reflowing one as prose rewrites the
// inner "/*" and "*/", which moves where the comment ends and pulls the
// SQL after it inside.
func TestNestedBlockCommentKeptVerbatim(t *testing.T) {
	src := "select 1;\n/*\n/*\n\n*/\n***/\nselect 2;\n"
	got := mustFormat(t, src)
	if !strings.Contains(got, "***/") {
		t.Errorf("nested comment terminator lost:\n%s", got)
	}
	if !strings.Contains(got, "select 2") {
		t.Errorf("SQL after the nested comment lost:\n%s", got)
	}
}

// A CREATE VIEW's body is a query and goes back through the query layout,
// which handles comments properly. Handing the whole statement to
// renderRun because of one comment inside that body flattened a rivered
// view into a single line.
func TestDDLWithQueryBodyKeepsItsLayout(t *testing.T) {
	src := "create or replace view v as\n" +
		"select a,\n       b\n  from t\n where a is not null -- keep\n   and b > 0;\n"
	got := mustFormat(t, src)
	if !strings.Contains(got, "-- keep") {
		t.Errorf("comment dropped:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > targetWidth {
			t.Errorf("view flattened past the margin: %q\n%s", line, got)
		}
	}
}
