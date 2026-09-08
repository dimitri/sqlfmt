package explain

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Parse every EXPLAIN plan in PostgreSQL's own regression expected output.
//
// The corpus this package was built against is one book's worth of plans
// over one schema: real, but narrow. PostgreSQL's src/test/regress/
// expected/*.out carries a couple of thousand plans written by the people
// who wrote the planner, covering partitioning, parallelism, incremental
// sort, memoize, foreign tables, custom scans, JIT, MERGE, recursive CTEs
// and every other corner the book never touches -- and every one of them
// is output a real server actually printed.
//
// Skipped unless PGREGRESS_DIR points at a PostgreSQL source tree, since
// it is not this repo's job to vendor one:
//
//	PGREGRESS_DIR=~/src/postgresql go test ./explain/ -run TestPGRegress -v
func TestPGRegressCorpus(t *testing.T) {
	root := os.Getenv("PGREGRESS_DIR")
	if root == "" {
		t.Skip("set PGREGRESS_DIR to a PostgreSQL source tree")
	}

	var files []string
	for _, pat := range []string{
		"src/test/regress/expected/*.out",
		"src/test/modules/*/expected/*.out",
		"contrib/*/expected/*.out",
	} {
		matches, _ := filepath.Glob(filepath.Join(root, pat))
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatalf("no expected/*.out under %s", root)
	}

	var (
		plans, parsed, failed, nodes int
		unknownProps                 = map[string]int{}
		knownProps, compositeProps   int
		failures                     = map[string]int{}
	)

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, block := range extractPlanBlocks(string(data)) {
			plans++
			p, err := Parse(block)
			if err != nil || p == nil || p.Root == nil {
				failed++
				if len(failures) < 40 {
					failures[firstLine(block)]++
				}
				continue
			}
			parsed++
			var walk func(n *Node)
			walk = func(n *Node) {
				nodes++
				for _, pr := range n.Props {
					if pr.Known() {
						knownProps++
						if len(pr.Fields) > 1 {
							compositeProps++
						}
					} else {
						unknownProps[propShape(pr.Raw)]++
					}
				}
				for _, c := range n.Children {
					walk(c)
				}
			}
			walk(p.Root)
		}
	}

	t.Logf("files=%d plans=%d parsed=%d failed=%d nodes=%d", len(files), plans, parsed, failed, nodes)
	t.Logf("props: known=%d composite=%d unknown=%d (%.1f%% known)",
		knownProps, compositeProps, sumCounts(unknownProps),
		100*float64(knownProps)/float64(knownProps+sumCounts(unknownProps)))

	for _, e := range topN(failures, 15) {
		t.Logf("  PARSE-FAIL %4d  %s", e.n, e.k)
	}
	for _, e := range topN(unknownProps, 25) {
		t.Logf("  UNKNOWN-PROP %5d  %s", e.n, e.k)
	}

	if parsed == 0 {
		t.Fatal("parsed nothing at all")
	}
}

// extractPlanBlocks pulls each "QUERY PLAN" block out of a psql
// transcript: the header, its separator rule, then every line up to the
// "(N rows)" footer or a blank line.
func extractPlanBlocks(s string) []string {
	var out []string
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "QUERY PLAN" {
			continue
		}
		var b strings.Builder
		b.WriteString(lines[i])
		b.WriteByte('\n')
		for j := i + 1; j < len(lines); j++ {
			b.WriteString(lines[j])
			b.WriteByte('\n')
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "(") && strings.HasSuffix(t, "rows)") {
				i = j
				break
			}
			if t == "" {
				i = j
				break
			}
		}
		out = append(out, b.String())
	}
	return out
}

func firstLine(block string) string {
	for _, l := range strings.Split(block, "\n") {
		if t := strings.TrimSpace(l); t != "" && t != "QUERY PLAN" && !strings.HasPrefix(t, "---") {
			if len(t) > 70 {
				t = t[:70]
			}
			return t
		}
	}
	return "(empty)"
}

// propShape reduces a property line to its label so the report groups by
// kind rather than by value.
func propShape(raw string) string {
	if label, _, ok := strings.Cut(raw, ": "); ok {
		if len(label) > 48 {
			label = label[:48]
		}
		return label + ": ..."
	}
	if len(raw) > 48 {
		raw = raw[:48]
	}
	return raw
}

type kv struct {
	k string
	n int
}

func topN(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n > out[j].n })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func sumCounts(m map[string]int) int {
	t := 0
	for _, v := range m {
		t += v
	}
	return t
}

var _ = fmt.Sprintf
