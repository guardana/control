//go:build ignore

// Command gen-index writes docs/index.md, the map of every page and record.
//
//	go run scripts/gen-index.go                    # stdout
//	go run scripts/gen-index.go -o docs/index.md   # file
//
// Everything this program decides lives in internal/docscheck/indexdoc, which
// the gate compiles, vets, lints and tests. What is left here is argument
// handling, a read of docs/docs.json for the exclusions, a walk and a write.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/indexdoc"
)

const config = "docs/docs.json"

func main() {
	out := flag.String("o", "", "write the page to this file instead of standard output")
	flag.Parse()
	if flag.NArg() != 0 {
		fail(fmt.Errorf("unexpected argument %q", flag.Arg(0)))
	}
	data, err := os.ReadFile(config)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", config, err))
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		fail(err)
	}
	excluded := func(rel string) bool {
		for _, prefix := range cfg.Excluded {
			if rel == prefix || strings.HasSuffix(prefix, "/") && strings.HasPrefix(rel, prefix) {
				return true
			}
		}
		return false
	}
	pages, records, err := indexdoc.Collect(os.DirFS("."), excluded)
	if err != nil {
		fail(err)
	}
	page, err := indexdoc.Render(pages, records)
	if err != nil {
		fail(err)
	}
	if err := write(*out, page); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "gen-index: %v\n", err)
	os.Exit(1)
}

func write(path string, page []byte) error {
	if path == "" {
		if _, err := os.Stdout.Write(page); err != nil {
			return fmt.Errorf("writing to standard output: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(path, page, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
