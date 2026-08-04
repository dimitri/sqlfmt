package format

import (
	"strings"
	"testing"
)

func lineComment(text string, leadingBlank bool) Token {
	return Token{Kind: TokLineComment, Text: text, Lower: text, LeadingBlank: leadingBlank}
}

func TestFormatLineCommentReflow(t *testing.T) {
	long := "-- " + strings.Repeat("word ", 30)
	got := formatLineComment(lineComment(long, false), 2)
	for _, l := range got {
		if len(l) > commentWidth {
			t.Errorf("line exceeds %d cols (%d): %q", commentWidth, len(l), l)
		}
		if !strings.HasPrefix(l, "  -- ") {
			t.Errorf("line missing indent+prefix: %q", l)
		}
	}
	if len(got) < 2 {
		t.Fatalf("expected reflow to produce multiple lines, got %d: %v", len(got), got)
	}
}

func TestFormatLineCommentDashDivider(t *testing.T) {
	got := formatLineComment(lineComment("-----------", false), 2)
	if len(got) != 1 || got[0] != "  -----------" {
		t.Fatalf("dash divider should be preserved verbatim, got %v", got)
	}
}

func TestFormatLineCommentEmpty(t *testing.T) {
	got := formatLineComment(lineComment("--", false), 0)
	if len(got) != 1 || got[0] != "--" {
		t.Fatalf("empty comment should render as bare --, got %v", got)
	}
}

func TestRenderLeadingCommentsBlankLineParagraphBreak(t *testing.T) {
	comments := []Token{
		lineComment("-- first paragraph", false),
		lineComment("-- second paragraph", true), // blank source line before it
	}
	got := renderLeadingComments(comments, 0)
	want := []string{"-- first paragraph", "", "-- second paragraph"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestFormatBlockCommentCStyle(t *testing.T) {
	c := Token{Kind: TokBlockComment, Text: "/* one two three */"}
	got := formatBlockComment(c, 2)
	want := []string{
		"  /*",
		"   * one two three",
		"   */",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q want %q", i, got[i], want[i])
		}
	}
	// star alignment: "/*"'s '*' and content lines' '*' must share a column.
	starCol := strings.IndexByte(got[0], '*')
	for _, l := range got[1:] {
		if strings.IndexByte(l, '*') != starCol {
			t.Errorf("star misaligned in %q (want col %d)", l, starCol)
		}
	}
}

func TestFormatBlockCommentReflowAndParagraphs(t *testing.T) {
	c := Token{Kind: TokBlockComment, Text: "/*\n * " + strings.Repeat("word ", 30) + "\n *\n * second paragraph\n */"}
	got := formatBlockComment(c, 0)
	if got[0] != "/*" || got[len(got)-1] != " */" {
		t.Fatalf("first/last line must contain only comment markers, got first=%q last=%q", got[0], got[len(got)-1])
	}
	for _, l := range got[1 : len(got)-1] {
		if l != "" && !strings.HasPrefix(l, " * ") {
			t.Errorf("content line missing ' * ' prefix: %q", l)
		}
		if len(l) > commentWidth {
			t.Errorf("line exceeds %d cols: %q", commentWidth, l)
		}
	}
	found := false
	for _, l := range got {
		if l == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a blank line between paragraphs, got %v", got)
	}
}

func TestAlignTrailingComments(t *testing.T) {
	text := "where a = 1" + commentMarker + "-- short\n" +
		"  and b = 22" + commentMarker + "-- also short\n"
	got := alignTrailingComments(text)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %v", lines)
	}
	col1 := strings.Index(lines[0], "--")
	col2 := strings.Index(lines[1], "--")
	if col1 != col2 {
		t.Errorf("trailing comments not aligned: %q vs %q", lines[0], lines[1])
	}
	if strings.Contains(got, commentMarker) {
		t.Errorf("marker not fully stripped: %q", got)
	}
}

func TestAlignTrailingCommentsBreaksAtNonCommentLine(t *testing.T) {
	text := "a" + commentMarker + "-- c1\n" +
		"gap line with no comment\n" +
		"bb" + commentMarker + "-- c2\n"
	got := alignTrailingComments(text)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	col1 := strings.Index(lines[0], "--")
	col2 := strings.Index(lines[2], "--")
	if col1 == col2 {
		t.Errorf("non-contiguous comment lines should NOT share a column: %q vs %q", lines[0], lines[2])
	}
}
