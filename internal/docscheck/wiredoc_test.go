package docscheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/guardana/control/internal/docscheck/wiredoc"
)

// The pin between the compiled contract and its pages. The renderer is
// called directly, never through `go run`, for the reason obligations_test.go
// gives. Before the byte comparison, every message, enum, field number and
// enum value the descriptor declares is looked for in the committed page on
// its own, so a renderer that dropped a row and pages regenerated from it
// cannot pass together; and every page under the directory has to be the
// page of a file, so a page of a file that left the contract does not stay.
func TestWireDocsAreCurrent(t *testing.T) {
	fsys := repoFS(t)
	files := wiredoc.Files()
	if len(files) == 0 {
		t.Fatal("the contract lists no file; nothing to pin")
	}
	rendered := map[string]bool{}
	for _, fd := range files {
		rendered[wiredoc.PageName(fd)] = true
		pinWirePage(t, fsys, fd)
	}
	entries, err := fs.ReadDir(fsys, wiredoc.Dir)
	if err != nil {
		t.Fatalf("reading %s: %v", wiredoc.Dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		if !rendered[e.Name()] {
			t.Errorf("%s/%s is the page of no file of the contract", wiredoc.Dir, e.Name())
		}
	}
}

// pinWirePage holds one committed page to its descriptor and to the renderer.
func pinWirePage(t *testing.T, fsys fs.FS, fd protoreflect.FileDescriptor) {
	t.Helper()
	rel := path.Join(wiredoc.Dir, wiredoc.PageName(fd))
	want, err := fs.ReadFile(fsys, rel)
	if err != nil {
		t.Errorf("reading %s: %v", rel, err)
		return
	}
	for _, problem := range missingDeclarations(fd, string(want)) {
		t.Errorf("%s: %s", rel, problem)
	}
	got, err := wiredoc.Page(fd)
	if err != nil {
		t.Errorf("rendering %s: %v", rel, err)
		return
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `%s`\n%s",
			rel, obligationsCommand, firstDifferingLine(got, want))
	}
}

// missingDeclarations names every message, enum, field and enum value of fd
// the page text does not carry as its own heading or row.
func missingDeclarations(fd protoreflect.FileDescriptor, text string) []string {
	var problems []string
	var messages func(protoreflect.MessageDescriptors, string)
	enums := func(list protoreflect.EnumDescriptors, prefix string) {
		for i := range list.Len() {
			e := list.Get(i)
			name := prefix + string(e.Name())
			if !strings.Contains(text, "\n### "+name+"\n") {
				problems = append(problems, "no heading for enum "+name)
			}
			for j := range e.Values().Len() {
				v := e.Values().Get(j)
				if row := fmt.Sprintf("| `%s` | %d |", v.Name(), v.Number()); !strings.Contains(text, row) {
					problems = append(problems, name+": no row "+row)
				}
			}
		}
	}
	messages = func(list protoreflect.MessageDescriptors, prefix string) {
		for i := range list.Len() {
			m := list.Get(i)
			if m.IsMapEntry() {
				continue
			}
			name := prefix + string(m.Name())
			if !strings.Contains(text, "\n### "+name+"\n") {
				problems = append(problems, "no heading for message "+name)
			}
			for j := range m.Fields().Len() {
				f := m.Fields().Get(j)
				if row := fmt.Sprintf("| `%s` | %d |", f.Name(), f.Number()); !strings.Contains(text, row) {
					problems = append(problems, name+": no row "+row)
				}
			}
			messages(m.Messages(), name+".")
			enums(m.Enums(), name+".")
		}
	}
	messages(fd.Messages(), "")
	enums(fd.Enums(), "")
	return problems
}
