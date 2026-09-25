package canon

import (
	"fmt"
	"strconv"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// readMessage is one message the digest reads, with the JSON pointer of the
// place it takes in the canonical action.
type readMessage struct {
	pointer string
	message proto.Message
}

// refuseUnknownFields refuses an envelope, or a message the digest reads, that
// carries a field this build does not know. It may be a field a later minor
// version added to the set, and the digest reads only the fields it knows, so
// two actions that differ only there would share one digest. Validate refuses
// the same input; the digest does not rely on every caller having validated
// the very object it is handed.
//
// The data labels, the arguments message and the run context are not checked:
// the digest reads none of them, whatever fields they carry.
func refuseUnknownFields(env *controlv1.ActionEnvelope) error {
	read := []readMessage{
		{"", env},
		{"/principal", env.GetPrincipal()},
		{"/agent", env.GetAgent()},
	}
	for i, hop := range env.GetDelegation() {
		read = append(read, readMessage{"/delegation/" + strconv.Itoa(i), hop})
	}
	read = append(read,
		readMessage{"/action", env.GetAction()},
		readMessage{"/resource", env.GetResource()},
		readMessage{"/destination", env.GetDestination()},
	)
	for _, m := range read {
		// IsValid is false for an absent message, which carries nothing.
		if r := m.message.ProtoReflect(); r.IsValid() && len(r.GetUnknown()) > 0 {
			return fmt.Errorf("%w at %q: the message carries an unknown field", ErrUnsupportedValue, m.pointer)
		}
	}
	return nil
}
