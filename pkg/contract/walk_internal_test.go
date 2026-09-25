// Package contract rather than contract_test: v1 has one Arguments and one
// Delegation, so a message sharing either short name cannot reach the walk
// through the exported surface, and the test hands it one built from a
// descriptor.
package contract

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestWalkExemptsOnlyTheEnvelopesOwnFreeText: the free-text rule and the
// preview's larger limit belong to two fields of this contract, matched on the
// generated types' full names. A message of the same short name in another
// package, which a later minor could bring under the envelope, is held to the
// identifier rule and to MaxStringBytes like any other.
func TestWalkExemptsOnlyTheEnvelopesOwnFreeText(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("lookalike/v1/lookalike.proto"),
		Package: proto.String("lookalike.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			lookalike("Arguments", "redacted_preview"),
			lookalike("Delegation", "reason"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("building the look-alike messages: %v", err)
	}
	for i := range file.Messages().Len() {
		md := file.Messages().Get(i)
		field := md.Fields().Get(0)
		for _, value := range []string{"a\U0000200bb", strings.Repeat("p", MaxStringBytes+1)} {
			m := dynamicpb.NewMessage(md)
			m.Set(field, protoreflect.ValueOfString(value))
			if err := walk(m, "", 0); err == nil {
				t.Errorf("%s.%s holding %.24q was accepted: it took the exemption of this contract's %s",
					md.FullName(), field.Name(), value, md.Name())
			}
		}
	}

	// The generated types keep theirs, so the rows above are refused for the
	// name and not because the walk refuses those values anywhere.
	for _, m := range []proto.Message{
		&controlv1.Arguments{RedactedPreview: "a\U0000200bb" + strings.Repeat("p", MaxStringBytes)},
		&controlv1.Delegation{Reason: "a\U0000200bb"},
	} {
		if err := walk(m.ProtoReflect(), "", 0); err != nil {
			t.Errorf("%s: its own free text is refused: %v", m.ProtoReflect().Descriptor().FullName(), err)
		}
	}
}

// lookalike describes a message holding one string field.
func lookalike(message, field string) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name: proto.String(message),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name:   proto.String(field),
			Number: proto.Int32(1),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}},
	}
}
