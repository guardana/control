// Package configdoc renders the configuration reference page from the
// gateway's tables, so the page lists exactly the keys the loader binds: a
// scalar key, a list or a map added to a table appears here at the next
// `make docs-gen`, and the pin test in internal/docscheck fails until it does.
//
// Rendering is a pure function of the tables. No clock, no filesystem:
// scripts/gen-config.go is the thin writer that puts the result on disk, and
// the pin test calls Page directly.
package configdoc

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/gatewayconfig"
)

// Generator is the script that writes the page, as the frontmatter names it.
const Generator = "scripts/gen-config.go"

// Path is where the page lives, relative to the repository root.
const Path = "docs/reference/configuration.md"

const (
	title            = "Configuration"
	summary          = "Every key the gateway reads, with its environment variable, kind, default, whether it is required and the spellings it takes."
	header           = "| Key | Variable | Kind | Default | Required | Values |\n| --- | --- | --- | --- | --- | --- |\n"
	collectionHeader = "| Key | Variable | One element |\n| --- | --- | --- |\n"
)

// Page renders the reference page for fields, which are the loader's own
// listing in its order, and for the lists and maps the loader binds. It
// refuses an empty listing, a key listed twice and a value a table row
// cannot carry.
func Page(fields []gatewayconfig.Field) ([]byte, error) {
	return render(fields, gatewayconfig.Collections())
}

func render(fields []gatewayconfig.Field, collections []gatewayconfig.Collection) ([]byte, error) {
	if len(fields) == 0 || len(collections) == 0 {
		return nil, errors.New("a table is empty; refusing to render a page listing no keys")
	}
	head, err := frontmatter.Render(frontmatter.Meta{
		Title:     title,
		Summary:   summary,
		Type:      "reference",
		Covers:    []string{"internal/gatewayconfig/**", "cmd/" + brand.Gateway + "/**"},
		Generated: Generator,
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(head)
	buf.WriteString("\n")
	buf.WriteString(lead())
	buf.WriteString(header)
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Path] {
			return nil, fmt.Errorf("%s is listed twice", f.Path)
		}
		seen[f.Path] = true
		row, err := renderRow(f)
		if err != nil {
			return nil, err
		}
		buf.WriteString(row)
	}
	buf.WriteString(between())
	buf.WriteString(collectionHeader)
	for _, c := range collections {
		if seen[c.Path] {
			return nil, fmt.Errorf("%s is listed twice", c.Path)
		}
		seen[c.Path] = true
		row, err := collectionRow(c)
		if err != nil {
			return nil, err
		}
		buf.WriteString(row)
	}
	buf.WriteString(tail())
	return buf.Bytes(), nil
}

func lead() string {
	return "# " + title + "\n\n" +
		"The gateway reads one file, named by `--config`, and then the environment: a variable\n" +
		"named `" + brand.EnvPrefix + "` followed by a key's path in upper case, with each dot an\n" +
		"underscore, sets that key and wins over the file. A key the table does not declare is\n" +
		"refused rather than ignored, and a required key with no default has to be set by the\n" +
		"file or by a variable. A key with a default is never without a value, so `yes` under\n" +
		"Required matters only where the default is empty. Some requirements depend on another\n" +
		"key and are not in the table: an HTTP listener needs `listener.address`; an upstream\n" +
		"needs exactly one of `endpoint` and `command`; `evidence.fsync_interval` is needed\n" +
		"under the `interval` policy and refused under `every_record`; the `file` approval\n" +
		"provider needs `approvals.dir` and `approvals.hold_journal_dir`, and that journal\n" +
		"directory may be neither the spool's nor the approvals directory, nor inside either;\n" +
		"and `APPROVE` needs the `file` provider, since nothing outside the process answers a\n" +
		"request held in memory. Every `pdp.` key is refused while `pdp.identifier` is empty,\n" +
		"since it would apply to no decision point, and the identifier is written into every\n" +
		"decision that consulted the decision point, so a credential goes in `pdp.headers`,\n" +
		"never in it. Directories are compared as paths and not as what they\n" +
		"reach: two paths that are one directory through a symbolic link are not refused, and\n" +
		"a pair this build cannot compare, such as one across volumes, is refused rather than\n" +
		"allowed.\n\n" +
		"Rendered from the field table in `internal/gatewayconfig`. Rebuild it with\n" +
		"`make docs-gen`; an edit made here does not survive the next run.\n\n"
}

func between() string {
	return "\nThese keys hold a list or a map, one element per key:\n\n"
}

func tail() string {
	n := gatewayconfig.IndexPlaceholder
	return "\n`" + n + "` in a key is an entry's index, from 0, and `" + gatewayconfig.NamePlaceholder + "` a name the operator\n" +
		"chooses. A variable for a key under `" + n + "` sets an entry the file declares; it never adds\n" +
		"one. In the variable of a header each `_` stands for a `-` in the header's name.\n" +
		"Header names are compared without case, so a variable wins over the file however\n" +
		"either spells the name. A name the file or the environment spells twice is refused,\n" +
		"and so is a `_` in a header's name in the file, since no variable could replace it.\n" +
		"A relative path resolves against the directory of the file.\n"
}

func collectionRow(c gatewayconfig.Collection) (string, error) {
	for _, cell := range []string{c.Path, c.Env, c.Holds} {
		if cell == "" {
			return "", fmt.Errorf("%q: a list or map key needs a path, a variable and what it holds", c.Path)
		}
		if err := checkCell(cell); err != nil {
			return "", fmt.Errorf("%s: %w", c.Path, err)
		}
	}
	return fmt.Sprintf("| `%s` | `%s` | %s |\n", c.Path, c.Env, c.Holds), nil
}

func renderRow(f gatewayconfig.Field) (string, error) {
	for _, cell := range append([]string{f.Path, f.Env, f.Kind, f.Default}, f.Values...) {
		if err := checkCell(cell); err != nil {
			return "", fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	if f.Path == "" || f.Env == "" || f.Kind == "" {
		return "", fmt.Errorf("%q: a key needs a path, a variable and a kind", f.Path)
	}
	required := "no"
	if f.Required {
		required = "yes"
	}
	return fmt.Sprintf("| `%s` | `%s` | %s | %s | %s | %s |\n",
		f.Path, f.Env, f.Kind, code(f.Default), required, values(f.Values)), nil
}

// code spells a value in code font, and nothing for an empty one.
func code(v string) string {
	if v == "" {
		return ""
	}
	return "`" + v + "`"
}

// values spells the admitted set. An empty spelling is a value the loader
// takes, so it is named rather than dropped.
func values(list []string) string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v == "" {
			out = append(out, "an empty value")
			continue
		}
		out = append(out, code(v))
	}
	return strings.Join(out, ", ")
}

// checkCell refuses text a table cell in code font cannot carry as it is:
// the pipe that ends the cell, the backtick that ends the code span, and
// every control, format or separator character.
func checkCell(s string) error {
	if i := strings.IndexFunc(s, breaksTheRow); i >= 0 {
		return fmt.Errorf("%q holds %U at byte %d, which a table row cannot carry", s, []rune(s[i:])[0], i)
	}
	return nil
}

func breaksTheRow(r rune) bool {
	return r == '|' || r == '`' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029
}
