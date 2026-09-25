package wiredoc

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fixture builds two files of one package the way the compiler would, so
// every shape a field can take is exercised: a scalar, a list, a map, an
// enum, a nested message, a message of the other file, a message of another
// package, a oneof member and a proto3 optional.
func fixture(t *testing.T) (protoreflect.FileDescriptor, protoreflect.FileDescriptor) {
	t.Helper()
	label := protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)
	other := &descriptorpb.FileDescriptorProto{
		Name: proto.String("wire/test/other.proto"), Package: proto.String("wire.test"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Remote")}},
	}
	optional := proto.Bool(true)
	main := &descriptorpb.FileDescriptorProto{
		Name: proto.String("wire/test/main.proto"), Package: proto.String("wire.test"), Syntax: proto.String("proto3"),
		Dependency: []string{"wire/test/other.proto", "google/protobuf/timestamp.proto"},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("Colour"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("COLOUR_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("COLOUR_RED"), Number: proto.Int32(7)},
			},
		}},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Outer"),
			Field: []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""),
				field("tags", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ""),
				field("labels", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_LABEL_REPEATED, ".wire.test.Outer.LabelsEntry"),
				field("colour", 4, descriptorpb.FieldDescriptorProto_TYPE_ENUM, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".wire.test.Colour"),
				field("inner", 5, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".wire.test.Outer.Inner"),
				field("remote", 6, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".wire.test.Remote"),
				field("at", 7, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ".google.protobuf.Timestamp"),
				oneofMember(field("left", 8, descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""), 0),
				oneofMember(field("right", 9, descriptorpb.FieldDescriptorProto_TYPE_BYTES, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""), 0),
				withOptional(oneofMember(field("score", 10, descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""), 1), optional),
			},
			OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("side")}, {Name: proto.String("_score")}},
			NestedType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("LabelsEntry"),
					Field: []*descriptorpb.FieldDescriptorProto{
						field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""),
						field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL, ""),
					},
					Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
				},
				{Name: proto.String("Inner"), EnumType: []*descriptorpb.EnumDescriptorProto{{
					Name:  proto.String("Depth"),
					Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("DEPTH_UNSPECIFIED"), Number: proto.Int32(0)}},
				}}},
			},
		}},
	}
	files := new(protoregistry.Files)
	var out []protoreflect.FileDescriptor
	for _, fdp := range []*descriptorpb.FileDescriptorProto{label, other, main} {
		fd, err := protodesc.NewFile(fdp, files)
		if err != nil {
			t.Fatalf("building %s: %v", fdp.GetName(), err)
		}
		if err := files.RegisterFile(fd); err != nil {
			t.Fatalf("registering %s: %v", fdp.GetName(), err)
		}
		out = append(out, fd)
	}
	return out[2], out[1]
}

func field(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type, label descriptorpb.FieldDescriptorProto_Label, typeName string) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(number), Type: typ.Enum(), Label: label.Enum(), JsonName: proto.String(name)}
	if typeName != "" {
		f.TypeName = proto.String(typeName)
	}
	return f
}

func oneofMember(f *descriptorpb.FieldDescriptorProto, index int32) *descriptorpb.FieldDescriptorProto {
	f.OneofIndex = proto.Int32(index)
	return f
}

func withOptional(f *descriptorpb.FieldDescriptorProto, optional *bool) *descriptorpb.FieldDescriptorProto {
	f.Proto3Optional = optional
	return f
}

func TestPageSpellsEveryShapeOfField(t *testing.T) {
	main, _ := fixture(t)
	page, err := Page(main)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	text := string(page)
	for _, want := range []string{
		"\n# main.proto\n",
		"\n## Messages\n",
		"\n### Outer\n",
		"| `id` | 1 | optional | `string` |",
		"| `tags` | 2 | repeated | `string` |",
		"| `labels` | 3 | map | map<`string`, `int64`> |",
		"| `colour` | 4 | optional | [`Colour`](#colour) |",
		"| `inner` | 5 | optional | [`Outer.Inner`](#outerinner) |",
		"| `remote` | 6 | optional | [`Remote`](other.md#remote) |",
		"| `at` | 7 | optional | `google.protobuf.Timestamp` |",
		"| `left` | 8 | oneof `side` | `int64` |",
		"| `right` | 9 | oneof `side` | `bytes` |",
		"| `score` | 10 | optional, presence tracked | `uint32` |",
		"\n### Outer.Inner\n\nNo fields.\n",
		"\n## Enums\n",
		"\n### Outer.Inner.Depth\n",
		"| `DEPTH_UNSPECIFIED` | 0 |",
		"\n### Colour\n",
		"| `COLOUR_RED` | 7 |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("page has no %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "LabelsEntry") {
		t.Error("page lists the entry message a map field is compiled to")
	}
	if strings.Contains(text, "wire.test.Outer") {
		t.Error("page spells a type of its own package by its full name")
	}
}

func TestPageCarriesTheFrontmatterTheCheckReads(t *testing.T) {
	_, other := fixture(t)
	page, err := Page(other)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	head := "---\ntitle: other.proto\n"
	if !strings.HasPrefix(string(page), head) {
		t.Errorf("page starts with %q, want %q", page[:min(len(page), len(head))], head)
	}
	for _, want := range []string{
		"\ntype: reference\n",
		"\ncovers: [api/proto/wire/test/other.proto]\n",
		"\ngenerated: " + Generator + "\n",
		"\n### Remote\n\nNo fields.\n",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("page has no %q\n%s", want, page)
		}
	}
	if strings.Contains(string(page), "## Enums") {
		t.Error("page has an Enums section and the file declares no enum")
	}
}

func TestPageRefusals(t *testing.T) {
	empty, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("wire/test/empty.proto"), Package: proto.String("wire.test"), Syntax: proto.String("proto3"),
	}, nil)
	if err != nil {
		t.Fatalf("building the empty file: %v", err)
	}
	noSuffix, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("wire/test/notes"), Package: proto.String("wire.test"), Syntax: proto.String("proto3"),
		EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("E"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("E_UNSPECIFIED"), Number: proto.Int32(0)}}}},
	}, nil)
	if err != nil {
		t.Fatalf("building the file without a suffix: %v", err)
	}
	for name, fd := range map[string]protoreflect.FileDescriptor{
		"no descriptor":             nil,
		"a file declaring nothing":  empty,
		"a path that is not .proto": noSuffix,
	} {
		t.Run(name, func(t *testing.T) {
			page, err := Page(fd)
			if err == nil {
				t.Fatalf("Page accepted %s:\n%s", name, page)
			}
			if page != nil {
				t.Errorf("Page returned %d bytes beside the error", len(page))
			}
		})
	}
}

func TestCheckNameRefusesWhatARowCannotCarry(t *testing.T) {
	for _, name := range []string{"", "a|b", "a`b", "a b", "a#b", "a[b", "a\nb"} {
		if err := checkName(name); err == nil {
			t.Errorf("checkName(%q) accepted it", name)
		}
	}
	for _, name := range []string{"Outer", "Outer.Inner", "snake_case", "v1"} {
		if err := checkName(name); err != nil {
			t.Errorf("checkName(%q): %v", name, err)
		}
	}
}

func TestPageIsDeterministic(t *testing.T) {
	main, _ := fixture(t)
	first, err := Page(main)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	second, err := Page(main)
	if err != nil {
		t.Fatalf("Page, second call: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two calls to Page produced different bytes")
	}
}

// TestFilesAreEveryFileOfThePackage: the listing is by hand, so a file added
// to the contract without a line here would have no page. The registry the
// generated code fills at init is the other listing, and the two agree.
func TestFilesAreEveryFileOfThePackage(t *testing.T) {
	files := Files()
	if len(files) == 0 {
		t.Fatal("Files lists nothing")
	}
	listed := map[string]bool{}
	for i, fd := range files {
		if fd == nil {
			t.Fatalf("Files()[%d] is nil", i)
		}
		if i > 0 && files[i-1].Path() >= fd.Path() {
			t.Errorf("%s comes after %s; the listing is not in path order", fd.Path(), files[i-1].Path())
		}
		listed[fd.Path()] = true
	}
	registered := 0
	protoregistry.GlobalFiles.RangeFilesByPackage(files[0].Package(), func(fd protoreflect.FileDescriptor) bool {
		registered++
		if !listed[fd.Path()] {
			t.Errorf("%s is registered under the package and Files does not list it", fd.Path())
		}
		return true
	})
	if registered != len(files) {
		t.Errorf("the registry holds %d file(s) of the package, Files lists %d", registered, len(files))
	}
}

func TestFindRefusesAFileNotInTheContract(t *testing.T) {
	first := Files()[0]
	fd, err := Find(PageName(first)[:len(PageName(first))-len(".md")] + ".proto")
	if err != nil || fd == nil || fd.Path() != first.Path() {
		t.Errorf("Find(the first file) = %v, %v", fd, err)
	}
	for _, name := range []string{"", "nothing.proto", first.Path(), "common"} {
		if fd, err := Find(name); err == nil {
			t.Errorf("Find(%q) returned %s, want a refusal", name, fd.Path())
		}
	}
}

func TestPageNameAndSourcePath(t *testing.T) {
	main, _ := fixture(t)
	if got := PageName(main); got != "main.md" {
		t.Errorf("PageName = %q, want main.md", got)
	}
	if got := SourcePath(main); got != "api/proto/wire/test/main.proto" {
		t.Errorf("SourcePath = %q", got)
	}
}
