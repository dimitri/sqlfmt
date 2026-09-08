package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dimitri/sqlfmt/explain"
)

// The "explain" subcommand namespace works on EXPLAIN *output* — the plan
// text a server printed — as opposed to the top-level command, which
// formats EXPLAIN *statements* along with the rest of your SQL.
//
// Why a subcommand here when the formatter itself is flags-and-paths,
// gofmt-style, and stays that way:
//
//   - The existing CLI is a published contract. "sqlfmt file.sql" formats,
//     and it has to keep formatting. Only a first argument of literally
//     "explain" diverts into this namespace; everything else takes the
//     original path unchanged.
//   - Plan work does not fit the formatter's shape. "sqlfmt [flags]
//     [path...]" means "do this to each of these files independently",
//     which is exactly wrong for a diff: two plans are one operation on a
//     PAIR, not two operations. Expressing that as flags (-diff a.txt
//     b.txt, positionally significant) would be worse than a subcommand.
//   - It leaves room. "explain diff" and "explain advice" are the two
//     that exist; the namespace can grow without adding more top-level
//     flags to a formatter.
//
// A file genuinely named "explain" is the one ambiguity, and "./explain"
// resolves it.
func runExplain(args []string) int {
	if len(args) == 0 {
		explainUsage()
		return 2
	}
	switch args[0] {
	case "advice":
		return runExplainAdvice(args[1:])
	case "diff":
		return runExplainDiff(args[1:])
	case "canonical":
		return runExplainCanonical(args[1:])
	case "json":
		return runExplainJSON(args[1:])
	case "help", "-h", "--help":
		explainUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "sqlfmt explain: unknown subcommand %q\n\n", args[0])
		explainUsage()
		return 2
	}
}

func explainUsage() {
	fmt.Fprint(os.Stderr, `usage: sqlfmt explain <command> [args]

Work on EXPLAIN output — the plan text a server printed.

commands:
  advice [plan]           print the plan's structure as PostgreSQL 19
                          plan advice; reads stdin when no file is given
  diff <before> <after>   compare two plans: what the planner decided
                          differently, and how the timings moved

  canonical [advice]      read an advice block that a server already
                          printed and normalize it the same way

  json [plan]             re-emit a TEXT plan as EXPLAIN (FORMAT JSON),
                          so a pasted plan can be handed to a tool that
                          only reads JSON. Emits only what the TEXT
                          actually carried: no Output list, no buffer
                          counters, no Plan Width unless they were on
                          the page.

Both advice and diff take -canonical, which normalizes advice before it is printed or
compared: set-valued targets are sorted and schema qualifiers dropped.
Use it when the two plans did not come from the same source — comparing
one of these reconstructions against PostgreSQL 19's own PLAN_ADVICE
output, say, where the two agree on the decisions but not on how they
write them down. Canonical output is a comparison key, not advice you can
feed back to a server.

Plans are read in psql's default TEXT format, with or without ANALYZE,
costs, the QUERY PLAN header, or a trailing row count.
`)
}

func runExplainAdvice(args []string) int {
	fs := flag.NewFlagSet("sqlfmt explain advice", flag.ExitOnError)
	canonical := fs.Bool("canonical", false,
		"normalize for comparison: sort set-valued targets, strip schema qualifiers")
	fs.Parse(args)

	var src []byte
	var name string
	var err error
	switch fs.NArg() {
	case 0:
		name = "<standard input>"
		src, err = io.ReadAll(os.Stdin)
	case 1:
		name = fs.Arg(0)
		src, err = os.ReadFile(name)
	default:
		fmt.Fprintln(os.Stderr, "usage: sqlfmt explain advice [plan]")
		return 2
	}
	if err != nil {
		report(err)
		return 2
	}

	plan, err := explain.Parse(string(src))
	if err != nil {
		report(fmt.Errorf("%s: %w", name, err))
		return 2
	}
	out := explain.AdviceString(plan)
	if *canonical {
		out = explain.CanonicalAdviceString(plan)
	}
	if out == "" {
		report(fmt.Errorf("%s: no plan advice could be derived", name))
		return 2
	}
	fmt.Println(out)
	return 0
}

// runExplainDiff exits 0 when the two plans are structurally identical and
// 1 when they are not, so it composes into scripts and CI the way diff(1)
// does. A real error is 2.
func runExplainDiff(args []string) int {
	fs := flag.NewFlagSet("sqlfmt explain diff", flag.ExitOnError)
	canonical := fs.Bool("canonical", false,
		"normalize both sides before comparing; use when the two plans did "+
			"not come from the same source")
	fs.Parse(args)

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: sqlfmt explain diff <before> <after>")
		return 2
	}
	before, err := parsePlanFile(fs.Arg(0))
	if err != nil {
		report(err)
		return 2
	}
	after, err := parsePlanFile(fs.Arg(1))
	if err != nil {
		report(err)
		return 2
	}

	d := explain.DiffPlans(before, after)
	if *canonical {
		d = explain.DiffPlansCanonical(before, after)
	}
	// Name the two sides after the files, the way diff(1) does: a diff
	// that does not say what it compared makes the reader remember.
	d.BeforeName = fs.Arg(0)
	d.AfterName = fs.Arg(1)
	fmt.Print(d.String())
	if d.SameStructure() {
		return 0
	}
	return 1
}

func parsePlanFile(path string) (*explain.Plan, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	plan, err := explain.Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return plan, nil
}

// runExplainCanonical normalizes advice that already exists, rather than
// deriving it from a plan.
//
// This is the other half of cross-source comparison. "explain advice
// -canonical" canonicalizes what this package reconstructs; this
// canonicalizes what PostgreSQL 19 itself printed, so the two can be put
// side by side:
//
//	diff <(sqlfmt explain advice -canonical pg16-plan.txt) \
//	     <(sqlfmt explain canonical pg19-plan.txt)
//
// Input may be a whole EXPLAIN (PLAN_ADVICE) capture — the advice block is
// found inside it — or just the advice lines.
func runExplainCanonical(args []string) int {
	fs := flag.NewFlagSet("sqlfmt explain canonical", flag.ExitOnError)
	fs.Parse(args)

	var src []byte
	var name string
	var err error
	switch fs.NArg() {
	case 0:
		name = "<standard input>"
		src, err = io.ReadAll(os.Stdin)
	case 1:
		name = fs.Arg(0)
		src, err = os.ReadFile(name)
	default:
		fmt.Fprintln(os.Stderr, "usage: sqlfmt explain canonical [advice]")
		return 2
	}
	if err != nil {
		report(err)
		return 2
	}

	out := explain.CanonicalAdviceTextFrom(string(src))
	if out == "" {
		report(fmt.Errorf("%s: no advice lines found", name))
		return 2
	}
	fmt.Println(out)
	return 0
}

// runExplainJSON re-emits a TEXT plan in EXPLAIN (FORMAT JSON) form.
//
// The point is interoperability with the tools that only read JSON --
// pgMustard, pev2's better-supported path -- from the plan a reader
// actually has in hand, which is nearly always the TEXT one psql
// printed. See explain.ToJSON on what it does and does not invent.
func runExplainJSON(args []string) int {
	fs := flag.NewFlagSet("sqlfmt explain json", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "usage: sqlfmt explain json [plan]") }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}

	var (
		src  []byte
		name string
		err  error
	)
	if fs.NArg() == 0 {
		name, src, err = "<standard input>", nil, nil
		src, err = io.ReadAll(os.Stdin)
	} else {
		name = fs.Arg(0)
		src, err = os.ReadFile(name)
	}
	if err != nil {
		report(err)
		return 2
	}

	plan, err := explain.Parse(string(src))
	if err != nil {
		report(fmt.Errorf("%s: %w", name, err))
		return 2
	}
	out, err := explain.ToJSON(plan)
	if err != nil {
		report(err)
		return 2
	}
	fmt.Println(string(out))
	return 0
}
