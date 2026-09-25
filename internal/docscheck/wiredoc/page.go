package wiredoc

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

const (
	fieldHeader = "| Field | Number | Cardinality | Type |\n| --- | --- | --- | --- |\n"
	valueHeader = "| Value | Number |\n| --- | --- |\n"
	specPage    = "../../contracts.md"
)

// Page renders the reference page of fd. It refuses a descriptor that is nil,
// that declares nothing, or whose path does not end in .proto, and a name a
// table row cannot carry.
func Page(fd protoreflect.FileDescriptor) ([]byte, error) {
	if err := check(fd); err != nil {
		return nil, err
	}
	name := path.Base(fd.Path())
	head, err := frontmatter.Render(frontmatter.Meta{
		Title:     name,
		Summary:   "The messages and enums of " + name + " as the compiled descriptor declares them, each field with its number, cardinality and type.",
		Type:      "reference",
		Covers:    []string{SourcePath(fd)},
		Generated: Generator,
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(head)
	buf.WriteString("\n")
	buf.WriteString(lead(fd))
	messages, enums := collect(fd.Messages(), fd.Enums(), "")
	if err := renderSections(&buf, messages, enums); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func check(fd protoreflect.FileDescriptor) error {
	switch {
	case fd == nil:
		return errors.New("no descriptor")
	case !strings.HasSuffix(fd.Path(), ".proto") || path.Base(fd.Path()) == ".proto":
		return fmt.Errorf("%q is not the path of a proto file", fd.Path())
	case fd.Messages().Len() == 0 && fd.Enums().Len() == 0:
		return fmt.Errorf("%s declares no message and no enum; refusing to render an empty page", fd.Path())
	}
	return nil
}

// renderSections writes the messages and then the enums, each under its own
// heading, and leaves out a section with nothing in it.
func renderSections(buf *bytes.Buffer, messages, enums []named) error {
	if len(messages) > 0 {
		buf.WriteString("\n## Messages\n")
		for _, m := range messages {
			if err := renderMessage(buf, m); err != nil {
				return err
			}
		}
	}
	if len(enums) > 0 {
		buf.WriteString("\n## Enums\n")
		for _, e := range enums {
			if err := renderEnum(buf, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func lead(fd protoreflect.FileDescriptor) string {
	name := path.Base(fd.Path())
	return "# " + name + "\n\n" +
		"What `" + name + "` of package `" + string(fd.Package()) + "` puts on the wire: every message with its\n" +
		"fields and every enum with its values, as the compiled descriptor declares them. The\n" +
		"descriptor carries no comments, so what a field means, when it is set and what a reader\n" +
		"does with a number it does not know is on [the wire contract](" + specPage + ").\n\n" +
		"Rendered from the descriptor compiled from `" + SourcePath(fd) + "`. Rebuild it with\n" +
		"`make docs-gen`; an edit made here does not survive the next run.\n"
}

// named is a message or enum with the name its section carries: the
// declaring messages' names before its own, joined by dots.
type named struct {
	name    string
	message protoreflect.MessageDescriptor
	enum    protoreflect.EnumDescriptor
}

// collect lists the messages and the enums in declaration order, a nested
// type after the message that declares it, and leaves out the entries a map
// field is compiled to.
func collect(messages protoreflect.MessageDescriptors, enums protoreflect.EnumDescriptors, prefix string) ([]named, []named) {
	var ms, es []named
	for i := range messages.Len() {
		m := messages.Get(i)
		if m.IsMapEntry() {
			continue
		}
		name := prefix + string(m.Name())
		ms = append(ms, named{name: name, message: m})
		nestedMessages, nestedEnums := collect(m.Messages(), m.Enums(), name+".")
		ms = append(ms, nestedMessages...)
		es = append(es, nestedEnums...)
	}
	for i := range enums.Len() {
		es = append(es, named{name: prefix + string(enums.Get(i).Name()), enum: enums.Get(i)})
	}
	return ms, es
}

func renderMessage(buf *bytes.Buffer, m named) error {
	if err := checkName(m.name); err != nil {
		return err
	}
	fmt.Fprintf(buf, "\n### %s\n\n", m.name)
	fields := m.message.Fields()
	if fields.Len() == 0 {
		buf.WriteString("No fields.\n")
		return nil
	}
	buf.WriteString(fieldHeader)
	for j := range fields.Len() {
		if err := renderField(buf, fields.Get(j)); err != nil {
			return fmt.Errorf("%s: %w", m.name, err)
		}
	}
	return nil
}

func renderField(buf *bytes.Buffer, f protoreflect.FieldDescriptor) error {
	if err := checkName(string(f.Name())); err != nil {
		return err
	}
	typ, err := typeOf(f)
	if err != nil {
		return err
	}
	fmt.Fprintf(buf, "| `%s` | %d | %s | %s |\n", f.Name(), f.Number(), cardinality(f), typ)
	return nil
}

// cardinality spells how many values a field holds: one, any number, a map,
// or one of a oneof's members. A proto3 field marked optional tracks whether
// it was set, which a reader of the wire can see, so it is named apart.
func cardinality(f protoreflect.FieldDescriptor) string {
	switch {
	case f.IsMap():
		return "map"
	case f.IsList():
		return "repeated"
	case f.HasOptionalKeyword():
		return "optional, presence tracked"
	case f.ContainingOneof() != nil && !f.ContainingOneof().IsSynthetic():
		return "oneof `" + string(f.ContainingOneof().Name()) + "`"
	}
	return "optional"
}

// typeOf spells a field's type: a scalar by its kind, a message or enum by
// its name linked to its section, a map by both of its parts.
func typeOf(f protoreflect.FieldDescriptor) (string, error) {
	if f.IsMap() {
		value, err := typeOf(f.MapValue())
		if err != nil {
			return "", err
		}
		return "map<`" + f.MapKey().Kind().String() + "`, " + value + ">", nil
	}
	switch f.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return link(f.Message(), f.ContainingMessage().ParentFile())
	case protoreflect.EnumKind:
		return link(f.Enum(), f.ContainingMessage().ParentFile())
	}
	return "`" + f.Kind().String() + "`", nil
}

// link names a message or enum and points at its section: on this page for
// a type of the same file, on the sibling page for a type of another file of
// the contract, and nowhere for a type of another package, which is named
// in full.
func link(d protoreflect.Descriptor, from protoreflect.FileDescriptor) (string, error) {
	file := d.ParentFile()
	if file.Package() != from.Package() {
		return "`" + string(d.FullName()) + "`", nil
	}
	name := strings.TrimPrefix(string(d.FullName()), string(file.Package())+".")
	if err := checkName(name); err != nil {
		return "", err
	}
	page := ""
	if file.Path() != from.Path() {
		page = PageName(file)
	}
	return "[`" + name + "`](" + page + "#" + anchor(name) + ")", nil
}

// anchor spells the fragment a heading of name gets: lower case, with the
// dots of a nested name dropped, as the link check in internal/docscheck
// computes it.
func anchor(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), ".", "")
}

func renderEnum(buf *bytes.Buffer, e named) error {
	if err := checkName(e.name); err != nil {
		return err
	}
	fmt.Fprintf(buf, "\n### %s\n\n", e.name)
	buf.WriteString(valueHeader)
	values := e.enum.Values()
	for j := range values.Len() {
		v := values.Get(j)
		if err := checkName(string(v.Name())); err != nil {
			return fmt.Errorf("%s: %w", e.name, err)
		}
		fmt.Fprintf(buf, "| `%s` | %d |\n", v.Name(), v.Number())
	}
	return nil
}

// checkName refuses a name a table cell in code font or a heading cannot
// carry as it is: the pipe, the backtick, a bracket, and every control,
// format or separator character. A descriptor the compiler accepted has
// none, so a refusal here is a descriptor nobody compiled.
func checkName(name string) error {
	if name == "" {
		return errors.New("an empty name")
	}
	if i := strings.IndexFunc(name, breaksTheRow); i >= 0 {
		return fmt.Errorf("%q holds %U at byte %d, which a table row cannot carry", name, []rune(name[i:])[0], i)
	}
	return nil
}

func breaksTheRow(r rune) bool {
	return strings.ContainsRune("|`[]()#", r) || unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}
