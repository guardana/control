// Package wiredoc renders one reference page per file of the wire contract
// from its compiled descriptor: every message with its fields, every enum
// with its values, with the names, numbers, cardinalities and types the
// descriptor declares. The generator strips the comments, so the meaning of
// a field stays on the spec page and each page says so.
//
// Rendering is a pure function of the descriptor. No clock, no filesystem:
// scripts/gen-wire.go is the thin writer that puts a page on disk, and the
// pin test in internal/docscheck calls Page directly.
package wiredoc

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// Generator is the script that writes a page, as the frontmatter names it.
const Generator = "scripts/gen-wire.go"

// Dir is where the pages live, relative to the repository root.
const Dir = "docs/reference/wire"

// protoRoot is where the descriptors' paths sit in the repository.
const protoRoot = "api/proto"

// Files is every file of the contract, in the order the pages list them:
// by path, so the order is the file names' and not the registration's.
func Files() []protoreflect.FileDescriptor {
	files := []protoreflect.FileDescriptor{
		controlv1.File_guardana_control_v1_action_envelope_proto,
		controlv1.File_guardana_control_v1_approval_proto,
		controlv1.File_guardana_control_v1_bundle_proto,
		controlv1.File_guardana_control_v1_common_proto,
		controlv1.File_guardana_control_v1_decision_proto,
		controlv1.File_guardana_control_v1_event_proto,
		controlv1.File_guardana_control_v1_finding_proto,
		controlv1.File_guardana_control_v1_result_proto,
	}
	slices.SortFunc(files, func(a, b protoreflect.FileDescriptor) int { return strings.Compare(a.Path(), b.Path()) })
	return files
}

// Find returns the file whose base name is name, such as "common.proto", and
// refuses a name no file of the contract carries.
func Find(name string) (protoreflect.FileDescriptor, error) {
	for _, fd := range Files() {
		if path.Base(fd.Path()) == name {
			return fd, nil
		}
	}
	return nil, fmt.Errorf("%q is not a file of the wire contract", name)
}

// PageName is the name of the page of fd under Dir: the proto file's base
// name with .md in place of .proto.
func PageName(fd protoreflect.FileDescriptor) string {
	return strings.TrimSuffix(path.Base(fd.Path()), ".proto") + ".md"
}

// SourcePath is the proto file's path in the repository, which the page
// covers.
func SourcePath(fd protoreflect.FileDescriptor) string {
	return protoRoot + "/" + fd.Path()
}
