package explain

import (
	"strings"
	"testing"
)

func TestCanonicalLine(t *testing.T) {
	cases := []struct {
		name string
		in   AdviceLine
		want string
		why  string
	}{
		{
			name: "set-valued targets are sorted",
			in:   AdviceLine{Kind: "NO_GATHER", Args: []string{"drivers", "results", "races"}},
			want: "NO_GATHER(drivers races results)",
			why: "19 emits NO_GATHER from a Bitmapset, so its order is range-table " +
				"order; this package walks the plan tree. Neither is recoverable " +
				"from the other and neither means anything, so both sort.",
		},
		{
			name: "JOIN_ORDER keeps its order",
			in:   AdviceLine{Kind: "JOIN_ORDER", Args: []string{"results races drivers"}},
			want: "JOIN_ORDER(results races drivers)",
			why:  "t1 drives and is joined to t2 before t3; sorting states something else",
		},
		{
			name: "JOIN_ORDER loses no relations to schema stripping",
			in:   AdviceLine{Kind: "JOIN_ORDER", Args: []string{"f1db.results f1db.drivers"}},
			want: "JOIN_ORDER(results drivers)",
			why: "the whole ordered list arrives as ONE pre-rendered arg, so a " +
				"stripSchema over the raw string returned just \"drivers\"",
		},
		{
			name: "JOIN_ORDER nested group stays ordered",
			in:   AdviceLine{Kind: "JOIN_ORDER", Args: []string{"t1 (t3 t2)"}},
			want: "JOIN_ORDER(t1 (t3 t2))",
			why:  "JOIN_ORDER(t1 (t2 t3)) puts t2 on the outer side of the inner join",
		},
		{
			name: "set-valued group is sorted inside and out",
			in:   AdviceLine{Kind: "HASH_JOIN", Args: []string{"(z y)", "a"}},
			want: "HASH_JOIN((y z) a)",
			why: "plain byte order, so groups sort ahead of bare names; there is " +
				"no ground truth to match here since 19 does not sort at all, " +
				"and the only thing that matters is that both sides agree",
		},
		{
			name: "index pairs sort together, schema stripped",
			in: AdviceLine{Kind: "INDEX_SCAN", Args: []string{
				"f", "public.join_fact_dim_id", "d", "public.join_dim_pkey"}},
			want: "INDEX_SCAN(d join_dim_pkey f join_fact_dim_id)",
			why:  "sorting the tokens flat would scramble relations against indexes",
		},
		{
			name: "index-only scan pairs likewise",
			in:   AdviceLine{Kind: "INDEX_ONLY_SCAN", Args: []string{"t", "public.t_pkey"}},
			want: "INDEX_ONLY_SCAN(t t_pkey)",
		},
		{
			name: "odd argument count is left alone",
			in:   AdviceLine{Kind: "INDEX_SCAN", Args: []string{"a", "a_idx", "b"}},
			want: "INDEX_SCAN(a a_idx b)",
			why:  "not the documented shape; reordering it would hide that",
		},
		{
			name: "occurrence numbering survives sorting",
			in:   AdviceLine{Kind: "SEQ_SCAN", Args: []string{"a#2", "a"}},
			want: "SEQ_SCAN(a a#2)",
		},
		{
			name: "relation schema stripped too",
			in:   AdviceLine{Kind: "SEQ_SCAN", Args: []string{"f1db.results"}},
			want: "SEQ_SCAN(results)",
			why:  "EXPLAIN (VERBOSE) qualifies relations; 19's identifiers never do",
		},
		{
			name: "empty group is untouched",
			in:   AdviceLine{Kind: "GATHER", Args: []string{"()"}},
			want: "GATHER(())",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in.Canonical().String()
			if got != c.want {
				t.Errorf("got  %s\nwant %s\n%s", got, c.want, c.why)
			}
		})
	}
}

// Canonical must not mutate its receiver: callers hold the original to
// print alongside the normalized form.
func TestCanonicalDoesNotMutate(t *testing.T) {
	in := AdviceLine{Kind: "NO_GATHER", Args: []string{"c", "a", "b"}}
	before := in.String()
	_ = in.Canonical()
	if in.String() != before {
		t.Errorf("receiver mutated: %s became %s", before, in.String())
	}
}

// Normalizing an already-normal line changes nothing, which is what makes
// it safe to canonicalize both sides of a comparison without knowing where
// either came from.
func TestCanonicalIsIdempotent(t *testing.T) {
	for _, l := range []AdviceLine{
		{Kind: "NO_GATHER", Args: []string{"c", "a", "b"}},
		{Kind: "JOIN_ORDER", Args: []string{"t1 (t3 t2)"}},
		{Kind: "INDEX_SCAN", Args: []string{"f", "public.i", "d", "public.j"}},
		{Kind: "HASH_JOIN", Args: []string{"(z y)", "a"}},
	} {
		once := l.Canonical()
		twice := once.Canonical()
		if once.String() != twice.String() {
			t.Errorf("not idempotent:\nonce  %s\ntwice %s", once.String(), twice.String())
		}
	}
}

// No relation may be dropped or invented. This is the invariant the
// JOIN_ORDER bug violated, and it is worth checking as a property rather
// than only on the case that happened to expose it.
func TestCanonicalPreservesTokens(t *testing.T) {
	for _, l := range []AdviceLine{
		{Kind: "JOIN_ORDER", Args: []string{"f1db.a f1db.b (f1db.c f1db.d)"}},
		{Kind: "NO_GATHER", Args: []string{"s.a", "s.b", "s.c"}},
		{Kind: "HASH_JOIN", Args: []string{"(s.z s.y)", "s.a"}},
		{Kind: "INDEX_SCAN", Args: []string{"s.f", "s.i", "s.d", "s.j"}},
	} {
		count := func(a AdviceLine) int {
			f := strings.FieldsFunc(strings.Join(a.Args, " "), func(r rune) bool {
				return r == ' ' || r == '(' || r == ')'
			})
			return len(f)
		}
		if got, want := count(l.Canonical()), count(l); got != want {
			t.Errorf("%s: %d targets in, %d out", l.Kind, want, got)
		}
	}
}

func TestStripSchema(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo_pkey", "foo_pkey"},
		{"public.foo_pkey", "foo_pkey"},
		{`public."weird idx"`, `"weird idx"`},
		{`"My Schema".foo`, "foo"},
		{`"a.b"`, `"a.b"`}, // one quoted identifier that contains a dot
		{"a#2", "a#2"},
		{"", ""},
		{"trailing.", "trailing."}, // nothing after the dot: not a qualifier
	}
	for _, c := range cases {
		if got := stripSchema(c.in); got != c.want {
			t.Errorf("stripSchema(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitTopLevelSpace(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a b c", []string{"a", "b", "c"}},
		{"a (b c)", []string{"a", "(b c)"}},
		{"(a (b c)) d", []string{"(a (b c))", "d"}},
		{"a  b", []string{"a", "b"}}, // runs of spaces do not make empty fields
		{"solo", []string{"solo"}},
	}
	for _, c := range cases {
		got := splitTopLevelSpace(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("split(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseAdviceLine(t *testing.T) {
	ok := []struct {
		in   string
		kind string
		args int
	}{
		{"HASH_JOIN(races drivers)", "HASH_JOIN", 2},
		{"   JOIN_ORDER(a b c)", "JOIN_ORDER", 3}, // 19 indents its block
		{"NO_GATHER(f)", "NO_GATHER", 1},
		{"GATHER((f d))", "GATHER", 1},
	}
	for _, c := range ok {
		l, got := ParseAdviceLine(c.in)
		if !got {
			t.Errorf("ParseAdviceLine(%q) rejected a valid line", c.in)
			continue
		}
		if l.Kind != c.kind || len(l.Args) != c.args {
			t.Errorf("ParseAdviceLine(%q) = %s with %d args, want %s with %d",
				c.in, l.Kind, len(l.Args), c.kind, c.args)
		}
	}

	// Everything a caller scanning a whole EXPLAIN capture must skip.
	bad := []string{
		"",
		"Generated Plan Advice:",
		" Hash Join",
		"   Hash Cond: (results.raceid = races.raceid)",
		"JOIN_ORDER(a b) /* matched */", // Supplied blocks carry a verdict
		"lower_case(a)",
		"HASH_JOIN(a",
		"HASH_JOIN(a))",
		"(a b)",
	}
	for _, s := range bad {
		if _, got := ParseAdviceLine(s); got {
			t.Errorf("ParseAdviceLine(%q) accepted a line that is not advice", s)
		}
	}
}

// The motivating case, end to end: a block PostgreSQL 19 printed and a
// block this package reconstructed from the same plan agree once both are
// canonical, and disagree before.
func TestCanonicalClosesTheCrossSourceGap(t *testing.T) {
	// The plan, as text EXPLAIN printed it.
	plan := ` Hash Join
   Hash Cond: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Hash
         ->  Index Scan using join_dim_pkey on join_dim d
`
	// What 19 itself printed for it: relations in range-table order, and
	// the index name schema-qualified.
	native := `Generated Plan Advice:
   JOIN_ORDER(f d)
   HASH_JOIN(d)
   SEQ_SCAN(f)
   INDEX_SCAN(d public.join_dim_pkey)
   NO_GATHER(d f)
`
	p := mustParse(t, plan)

	if AdviceString(p) == CanonicalAdviceTextFrom(native) {
		t.Fatal("fixture no longer exercises a cross-source difference")
	}
	got := CanonicalAdviceString(p)
	want := CanonicalAdviceTextFrom(native)
	if got != want {
		t.Errorf("canonical forms still differ:\nreconstructed:\n%s\nnative:\n%s", got, want)
	}
}

// A "Supplied Plan Advice" block must not be mistaken for a generated one:
// its lines end in a /* matched */ verdict rather than ")".
func TestCanonicalAdviceTextSkipsSuppliedBlocks(t *testing.T) {
	in := `Supplied Plan Advice:
   JOIN_ORDER(drivers results races) /* matched */
   HASH_JOIN(races) /* matched, failed */
Generated Plan Advice:
   JOIN_ORDER(a b)
`
	got := CanonicalAdviceTextFrom(in)
	if got != "JOIN_ORDER(a b)" {
		t.Errorf("got %q, want only the generated line", got)
	}
}

func TestDiffCanonicalIsQuietWhenOnlyNotationDiffers(t *testing.T) {
	// Same shape, but VERBOSE on one side: relations arrive qualified.
	plain := mustParse(t, ` Nested Loop
   ->  Seq Scan on results
   ->  Index Scan using drivers_pkey on drivers
`)
	verbose := mustParse(t, ` Nested Loop
   ->  Seq Scan on f1db.results
   ->  Index Scan using drivers_pkey on f1db.drivers
`)

	if d := DiffPlans(plain, verbose); d.SameStructure() {
		t.Fatal("fixture no longer exercises a notational difference")
	}
	d := DiffPlansCanonical(plain, verbose)
	if !d.SameStructure() {
		t.Errorf("canonical diff should be quiet, got:\n%s", d.String())
	}
	if !strings.Contains(d.String(), "canonical") {
		t.Errorf("a canonical diff must say so in its hunk header:\n%s", d.String())
	}
}

// Canonicalizing must not hide a real change.
func TestDiffCanonicalStillSeesRealChanges(t *testing.T) {
	before := mustParse(t, ` Hash Join
   Hash Cond: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Hash
         ->  Seq Scan on join_dim d
`)
	after := mustParse(t, ` Nested Loop
   ->  Seq Scan on join_fact f
   ->  Index Scan using join_dim_pkey on join_dim d
`)
	d := DiffPlansCanonical(before, after)
	if d.SameStructure() {
		t.Error("a hash join becoming a nested loop is a real change")
	}
}
