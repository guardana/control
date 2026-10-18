package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
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

// guideCommands is every command a code block of the guide runs through one
// of the product's binaries, as the binary and its subcommand words.
func guideCommands(t *testing.T, text string) []string {
	t.Helper()
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		fields := strings.Fields(line)
		if !fenced || len(fields) == 0 || (fields[0] != brand.CLI && fields[0] != brand.Gateway) {
			continue
		}
		words := []string{fields[0]}
		for _, f := range fields[1:] {
			if !subcommandWord.MatchString(f) {
				break
			}
			words = append(words, f)
		}
		out = append(out, strings.Join(words, " "))
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no command of either binary in a code block", mcpClientGuide)
	}
	return out
}

// TestTheMCPClientCheckRunsEveryCommandOfTheGuide: the check that runs the
// guide end to end runs each command the guide's code blocks name, so a step
// added to the guide fails here until the check runs it too.
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
	ran := strings.NewReplacer(`"${control}"`, brand.CLI, `"${gateway}"`, brand.Gateway).Replace(string(script))
	for _, command := range guideCommands(t, string(guide)) {
		if !regexp.MustCompile(`(?m)^\s*`+regexp.QuoteMeta(command)+`(\s|$)`).MatchString(ran) &&
			!regexp.MustCompile(`\$\(`+regexp.QuoteMeta(command)+`(\s|\))`).MatchString(ran) {
			t.Errorf("%s runs %q, and %s does not", mcpClientGuide, command, mcpClientScript)
		}
	}
}
