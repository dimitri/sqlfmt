package explain

import "testing"

// BitmapAnd and BitmapOr sit between a Bitmap Heap Scan and its Bitmap
// Index Scan children. They were absent from the token table, so they
// parsed as "unknown" and every renderer labelled them "Node" — the
// generic fallback, on precisely the node a reader is looking up when
// asking whether an OR predicate used its indexes.
func TestBitmapCombiningNodesAreNamed(t *testing.T) {
	p, err := Parse(` Bitmap Heap Scan on results  (cost=17.76..446.98 rows=1142 width=40)
   Recheck Cond: ((points = '25'::double precision) OR (statusid = 3))
   ->  BitmapOr  (cost=17.76..17.76 rows=1142 width=0)
         ->  Bitmap Index Scan on results_points_idx  (cost=0.00..4.34 rows=112 width=0)
               Index Cond: (points = '25'::double precision)
         ->  Bitmap Index Scan on results_statusid_idx  (cost=0.00..12.85 rows=1030 width=0)
               Index Cond: (statusid = 3)
`)
	if err != nil {
		t.Fatal(err)
	}
	or := p.Root.Children[0]
	if or.Type != "bitmap-or" {
		t.Errorf("type = %q, want bitmap-or", or.Type)
	}
	if got := NodeLabel(or); got != "BitmapOr" {
		t.Errorf("label = %q, want BitmapOr", got)
	}
	if len(or.Children) != 2 {
		t.Fatalf("BitmapOr has %d children, want 2", len(or.Children))
	}
}

func TestBitmapAndSpellings(t *testing.T) {
	for _, tok := range []string{"BitmapAnd", "Bitmap And"} {
		typ, _, _, _ := matchNodeType(tok + "  (cost=0.00..1.00 rows=1 width=0)")
		if typ != "bitmap-and" {
			t.Errorf("%q -> %q, want bitmap-and", tok, typ)
		}
	}
	for _, tok := range []string{"BitmapOr", "Bitmap Or"} {
		typ, _, _, _ := matchNodeType(tok + "  (cost=0.00..1.00 rows=1 width=0)")
		if typ != "bitmap-or" {
			t.Errorf("%q -> %q, want bitmap-or", tok, typ)
		}
	}
}
