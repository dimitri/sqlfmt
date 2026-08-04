package format

import "testing"

func lexOK(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := Lex([]byte(src))
	if err != nil {
		t.Fatalf("Lex(%q) error: %v", src, err)
	}
	return toks
}

func TestLexDollarQuoting(t *testing.T) {
	toks := lexOK(t, `select $$hello world$$;`)
	if toks[1].Kind != TokDollarString || toks[1].Text != "$$hello world$$" {
		t.Fatalf("got %+v", toks[1])
	}

	toks = lexOK(t, `select $tag$it's $$ inside$tag$;`)
	if toks[1].Kind != TokDollarString || toks[1].Text != "$tag$it's $$ inside$tag$" {
		t.Fatalf("got %+v", toks[1])
	}
}

func TestLexNestedBlockComment(t *testing.T) {
	toks := lexOK(t, "select /* outer /* inner */ still outer */ 1;")
	if toks[1].Kind != TokBlockComment {
		t.Fatalf("got %+v", toks[1])
	}
	if toks[1].Text != "/* outer /* inner */ still outer */" {
		t.Fatalf("nesting not respected: %q", toks[1].Text)
	}
}

func TestLexStringEscape(t *testing.T) {
	toks := lexOK(t, `select 'it''s' ;`)
	if toks[1].Kind != TokString || toks[1].Text != `'it''s'` {
		t.Fatalf("got %+v", toks[1])
	}
}

func TestLexMultiCharOperators(t *testing.T) {
	src := "a::int || b ->> c @> d <@ e <= f >= g <> h != i -> j"
	toks := lexOK(t, src)
	want := []string{"a", "::", "int", "||", "b", "->>", "c", "@>", "d", "<@", "e", "<=", "f", ">=", "g", "<>", "h", "!=", "i", "->", "j"}
	if len(toks)-1 != len(want) {
		t.Fatalf("token count = %d, want %d: %+v", len(toks)-1, len(want), toks)
	}
	for i, w := range want {
		if toks[i].Text != w {
			t.Fatalf("token %d = %q, want %q", i, toks[i].Text, w)
		}
	}
}

func TestLexBackslashCommand(t *testing.T) {
	toks := lexOK(t, "\\set season 'date ''1978-01-01'''\nselect 1;")
	if toks[0].Kind != TokBackslashCmd {
		t.Fatalf("got %+v", toks[0])
	}
	if toks[1].Kind != TokKeyword || toks[1].Lower != "select" {
		t.Fatalf("got %+v", toks[1])
	}
}

func TestLexKeywordCaseInsensitive(t *testing.T) {
	toks := lexOK(t, "SELECT Foo FROM bar")
	if toks[0].Kind != TokKeyword || toks[0].Lower != "select" || toks[0].Text != "SELECT" {
		t.Fatalf("got %+v", toks[0])
	}
	if toks[1].Kind != TokIdent || toks[1].Text != "Foo" {
		t.Fatalf("got %+v", toks[1])
	}
}

func TestLexQuotedIdentifier(t *testing.T) {
	toks := lexOK(t, `select "Group" as "my ""alias"""`)
	if toks[1].Kind != TokIdent || toks[1].Text != `"Group"` {
		t.Fatalf("got %+v", toks[1])
	}
}

func TestLexBlankLineTracking(t *testing.T) {
	toks := lexOK(t, "select 1;\n\ncommit;")
	// find the "commit" token
	var commit Token
	for _, tok := range toks {
		if tok.Lower == "commit" {
			commit = tok
		}
	}
	if !commit.LeadingBlank {
		t.Fatalf("expected LeadingBlank on commit token, got %+v", commit)
	}
}

func TestLexUnterminatedStringError(t *testing.T) {
	if _, err := Lex([]byte(`select 'unterminated`)); err == nil {
		t.Fatal("expected error for unterminated string")
	}
}
