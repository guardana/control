//go:build ignore

// Command gen-site redraws the diagram slots of the site's landing page from
// the README's Mermaid blocks.
//
//	go run scripts/gen-site.go -o site/index.html
//
// Everything it decides lives in internal/docscheck/sitedoc, where the gate
// compiles, vets, lints and tests it. What is left here is argument handling,
// two reads and one write of the same page.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/guardana/control/internal/docscheck/sitedoc"
)

func main() {
	out := flag.String("o", "", "the page whose slots are rewritten in place (required)")
	flag.Parse()
	if flag.NArg() != 0 {
		fail(fmt.Errorf("unexpected argument %q", flag.Arg(0)))
	}
	if *out == "" {
		fail(errors.New("-o names the page; a figure is not rendered on its own"))
	}
	if *out != sitedoc.Page {
		fail(fmt.Errorf("%s is not %s, the one page this program rewrites", *out, sitedoc.Page))
	}
	page, err := os.ReadFile(*out)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", *out, err))
	}
	readme, err := os.ReadFile(sitedoc.Source)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", sitedoc.Source, err))
	}
	rendered, err := sitedoc.Render(page, readme)
	if err != nil {
		fail(fmt.Errorf("%s: %w", *out, err))
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fail(fmt.Errorf("writing %s: %w", *out, err))
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "gen-site: %v\n", err)
	os.Exit(1)
}
