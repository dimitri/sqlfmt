package format

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// knownDivergent lists corpus fixtures that are known, documented
// exceptions to round-tripping and must not be made to pass mechanically.
// It is empty: testdata/corpus/ holds sqlfmt's own canonical output over
// real book queries (each file was originally hand-formatted, then passed
// through sqlfmt and reviewed), so TestCorpusRoundTrip is chiefly an
// idempotency/regression check -- not an independent judge of STYLE.md
// fidelity. A genuinely unreproducible hand-formatting quirk (e.g. the old
// sql-103-04_01.sql column-alignment inconsistency) is expected to
// disappear once a fixture is regenerated this way; if a future edit to a
// corpus file reintroduces real drift from the tool's own output, name the
// file here with the reason so the test documents it instead of silently
// failing.
var knownDivergent = map[string]string{}

func TestCorpusRoundTrip(t *testing.T) {
	root := "../testdata/corpus"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		t.Run(rel, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Format(bytes.NewReader(src))
			if err != nil {
				t.Fatalf("Format error: %v", err)
			}
			if reason, known := knownDivergent[rel]; known {
				if got == string(src) {
					t.Fatalf("%s round-tripped but is listed as knownDivergent (%s) -- remove it from the map", rel, reason)
				}
				t.Skipf("known non-round-tripping fixture: %s", reason)
				return
			}
			if got != string(src) {
				t.Errorf("round-trip mismatch for %s:\n--- want ---\n%s\n--- got ---\n%s", rel, src, got)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFullCorpusIfAvailable optionally validates against the full 343-file
// sibling corpus this style was derived from, when SQLFMT_CORPUS_DIR points
// at a local checkout of TheArtOfPostgreSQL. Mismatches are logged, not
// failed -- that corpus was never curated to be 100% round-trip-clean.
func TestFullCorpusIfAvailable(t *testing.T) {
	dir := os.Getenv("SQLFMT_CORPUS_DIR")
	if dir == "" {
		t.Skip("SQLFMT_CORPUS_DIR not set")
	}
	total, mismatched := 0, 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		total++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, ferr := Format(bytes.NewReader(src))
		if ferr != nil {
			mismatched++
			t.Logf("format error %s: %v", path, ferr)
			return nil
		}
		if got != string(src) {
			mismatched++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("full corpus: %d/%d files round-tripped", total-mismatched, total)
}
