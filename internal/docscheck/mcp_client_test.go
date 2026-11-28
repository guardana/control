package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

const (
	mcpClientGuide  = "docs/guides/connect-an-existing-mcp-setup.md"
	mcpClientScript = "scripts/check-mcp-client.sh"
)

// subcommandWord is one word of a command's name, before its first flag,
// path or placeholder.
var subcommandWord = regexp.MustCompile(`^[a-z]+$`)

// guideCommand is one command a code block of the guide runs: the binary,
// its subcommand words and the token after them, so "trail <file>" and
// "trail export" stay two commands, and the flags the line passes.
type guideCommand struct {
	prefix string
	flags  []string
}

// joinedLines is text with each line that ends in a backslash joined to the
// next, as a shell reads it.
func joinedLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutSuffix(line, `\`); ok {
			cur.WriteString(rest + " ")
			continue
		}
		cur.WriteString(line)
		out = append(out, cur.String())
		cur.Reset()
	}
	return out
}

func isBinary(field string) bool { return field == brand.CLI || field == brand.Gateway }

func flagsOf(fields []string) []string {
	var out []string
	for _, f := range fields {
		if name, ok := strings.CutPrefix(f, "--"); ok && name != "" {
			name, _, _ = strings.Cut(name, "=")
			out = append(out, "--"+name)
		}
	}
	return out
}

// guideCommands is every command a code block of the guide runs through one
// of the product's binaries. A binary named anywhere but at the start of a
// command line, behind a prefix such as sudo, is reported, since this test
// could not hold the script to it.
func guideCommands(t *testing.T, text string) []guideCommand {
	t.Helper()
	var out []guideCommand
	fenced := false
	for _, line := range joinedLines(text) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(trimmed, "$ "))
		if !fenced || len(fields) == 0 {
			continue
		}
		if !isBinary(fields[0]) {
			if slices.ContainsFunc(fields, isBinary) {
				t.Errorf("%s runs a binary behind a prefix this test cannot follow: %q", mcpClientGuide, trimmed)
			}
			continue
		}
		words := []string{fields[0]}
		for _, f := range fields[1:] {
			words = append(words, f)
			if !subcommandWord.MatchString(f) {
				break
			}
		}
		out = append(out, guideCommand{prefix: strings.Join(words, " "), flags: flagsOf(fields)})
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no command of either binary in a code block", mcpClientGuide)
	}
	return out
}

// TestTheMCPClientCheckRunsEveryCommandOfTheGuide: the check that runs the
// guide end to end names each command the guide's code blocks run, with
// every flag the guide passes it, so a step added to the guide, or a flag,
// fails here until the check names it too. It compares text: a command the
// script names but never reaches still passes.
func TestTheMCPClientCheckRunsEveryCommandOfTheGuide(t *testing.T) {
	root := repoRoot(t)
	guide, err := os.ReadFile(filepath.Join(root, mcpClientGuide)) //nolint:gosec // G304: a path this repository names
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, mcpClientScript)) //nolint:gosec // G304: a path this repository names
	if err != nil {
		t.Fatal(err)
	}
	names := strings.NewReplacer(`"${control}"`, brand.CLI, `"${gateway}"`, brand.Gateway)
	var lines []string
	for _, line := range joinedLines(string(script)) {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, names.Replace(line))
		}
	}
	for _, command := range guideCommands(t, string(guide)) {
		named := regexp.MustCompile(`(^\s*|\$\()` + regexp.QuoteMeta(command.prefix) + `(\s|\)|$)`)
		if !slices.ContainsFunc(lines, func(line string) bool {
			return named.MatchString(line) && !slices.ContainsFunc(command.flags, func(f string) bool {
				return !slices.Contains(flagsOf(strings.Fields(line)), f)
			})
		}) {
			t.Errorf("%s runs %q with %v, and %s names no such line", mcpClientGuide, command.prefix, command.flags, mcpClientScript)
		}
	}
}

// TestTheMCPClientCheckKeepsTheCredentialStepsOfTheGuide: the guide closes the
// plane's directory and exports the collector's credential in every code block
// that runs doctor or the plane, and the check closes the directory, verifies
// its mode and exports the credential, so neither can drop one without failing
// here.
func TestTheMCPClientCheckKeepsTheCredentialStepsOfTheGuide(t *testing.T) {
	root := repoRoot(t)
	guide, err := os.ReadFile(filepath.Join(root, mcpClientGuide)) //nolint:gosec // G304: a path this repository names
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, mcpClientScript)) //nolint:gosec // G304: a path this repository names
	if err != nil {
		t.Fatal(err)
	}
	variable := regexp.QuoteMeta(brand.Env("EXPORT_HEADERS_AUTHORIZATION"))
	exported := regexp.MustCompile(`^export ` + variable + `="Bearer \$\(cat collector\.credential\)"$`)
	if !regexp.MustCompile(`(?m)^chmod 700 ~/files-plane$`).Match(guide) {
		t.Errorf("%s does not close the plane's directory with chmod 700 ~/files-plane", mcpClientGuide)
	}
	runs := 0
	for _, block := range codeBlocks(string(guide)) {
		set := false
		for _, line := range block {
			fields := strings.Fields(line)
			set = set || exported.MatchString(line)
			if len(fields) > 1 && fields[0] == brand.Gateway && (fields[1] == "doctor" || fields[1] == "run") {
				runs++
				if !set {
					t.Errorf("%s runs %q in a code block that does not first export %s", mcpClientGuide, line, variable)
				}
			}
		}
	}
	if runs < 2 {
		t.Errorf("%s runs doctor and the plane %d time(s) in its code blocks, want both", mcpClientGuide, runs)
	}
	for _, line := range []string{
		`(?m)^chmod 700 "\$\{work\}/plane"$`,
		`(?m)^\[\[ -n "\$\(find \. -maxdepth 0 -perm 700\)" \]\] \|\| die `,
		`(?m)^export ` + variable + `$`,
	} {
		if !regexp.MustCompile(line).Match(script) {
			t.Errorf("%s holds no line matching %s", mcpClientScript, line)
		}
	}
}

// codeBlocks is each fenced code block of text, as its trimmed lines with a
// leading "$ " prompt removed.
func codeBlocks(text string) [][]string {
	var out [][]string
	var cur []string
	fenced := false
	for _, line := range joinedLines(text) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if fenced {
				out = append(out, cur)
				cur = nil
			}
			fenced = !fenced
			continue
		}
		if fenced {
			cur = append(cur, strings.TrimPrefix(trimmed, "$ "))
		}
	}
	return out
}
