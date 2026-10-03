//go:build ignore

// Command gen-site redraws the diagram slots of the site's landing page from
// the README's Mermaid blocks, writes the newest release from CHANGELOG.md
// into its release slot, and renders the documentation into site/docs/ with
// the sitemap.
//
//	go run scripts/gen-site.go -o site/index.html
//
// Everything it decides lives in internal/docscheck/sitedoc and its docsite
// package, where the gate compiles, vets, lints and tests it. What is left here
// is argument handling, reads, and the writes: site/docs/ is removed and
// written again whole, so a page whose source is gone goes with it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/sitedoc"
	"github.com/guardana/control/internal/docscheck/sitedoc/docsite"
)

const config = "docs/docs.json"

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
	notes, err := os.ReadFile(sitedoc.Changelog)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", sitedoc.Changelog, err))
	}
	release, err := sitedoc.Release(notes)
	if err != nil {
		fail(err)
	}
	if rendered, err = sitedoc.FillRelease(rendered, release); err != nil {
		fail(fmt.Errorf("%s: %w", *out, err))
	}
	data, err := os.ReadFile(config)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", config, err))
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		fail(err)
	}
	files, err := docsite.Build(os.DirFS("."), cfg.Excludes)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fail(fmt.Errorf("writing %s: %w", *out, err))
	}
	if err := os.RemoveAll(docsite.Dir); err != nil {
		fail(fmt.Errorf("removing %s: %w", docsite.Dir, err))
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(name, files[name], 0o644); err != nil {
			fail(fmt.Errorf("writing %s: %w", name, err))
		}
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "gen-site: %v\n", err)
	os.Exit(1)
}
