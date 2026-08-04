// Command sqlfmt formats PostgreSQL SQL, gofmt-style.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/dimitri/sqlfmt/format"
)

var (
	write  = flag.Bool("w", false, "write result to (source) file instead of stdout")
	list   = flag.Bool("l", false, "list files whose formatting differs from sqlfmt's")
	doDiff = flag.Bool("d", false, "display diffs instead of rewriting files")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: sqlfmt [flags] [path ...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		if err := processReader(os.Stdin, "<standard input>"); err != nil {
			report(err)
			os.Exit(2)
		}
		return
	}

	exitCode := 0
	for _, arg := range flag.Args() {
		info, err := os.Stat(arg)
		if err != nil {
			report(err)
			exitCode = 2
			continue
		}
		if info.IsDir() {
			werr := filepath.WalkDir(arg, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if strings.HasPrefix(d.Name(), ".") && path != arg {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasSuffix(path, ".sql") {
					if err := processFile(path); err != nil {
						report(err)
						exitCode = 2
					}
				}
				return nil
			})
			if werr != nil {
				report(werr)
				exitCode = 2
			}
			continue
		}
		if err := processFile(arg); err != nil {
			report(err)
			exitCode = 2
		}
	}
	os.Exit(exitCode)
}

func report(err error) {
	fmt.Fprintln(os.Stderr, err)
}

func processReader(r io.Reader, name string) error {
	src, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	formatted, err := format.Format(bytes.NewReader(src))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if *list {
		return nil
	}
	if *doDiff {
		return printDiff(name, string(src), formatted)
	}
	_, err = os.Stdout.WriteString(formatted)
	return err
}

func processFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	formatted, err := format.Format(bytes.NewReader(src))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	changed := string(src) != formatted

	if !*list && !*doDiff && !*write {
		_, err = os.Stdout.WriteString(formatted)
		return err
	}
	if *list && changed {
		fmt.Println(path)
	}
	if *doDiff && changed {
		if err := printDiff(path, string(src), formatted); err != nil {
			return err
		}
	}
	if *write && changed {
		return os.WriteFile(path, []byte(formatted), info.Mode())
	}
	return nil
}

func printDiff(name, before, after string) error {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: name + ".orig",
		ToFile:   name,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(text)
	return err
}
