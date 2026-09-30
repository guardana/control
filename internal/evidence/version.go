package evidence

import (
	"fmt"
	"strconv"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// maxQuotedVersionBytes bounds how much of a refused version a refusal quotes.
const maxQuotedVersionBytes = 32

// CheckEventVersion refuses an event this build cannot read by its
// schema_version: none, one that is not MAJOR.MINOR in decimal, or a major
// other than the one the generated types carry. A higher minor is admitted.
// The refusal wraps contract.ErrUnsupportedSchema.
//
// It is the rule pkg/contract holds an envelope to, kept apart from the codec:
// the codec carries a record it does not judge, and a reader that acts on the
// record's meaning asks here first. testdata/contracts/schema_versions.json
// holds both rules to one table.
func CheckEventVersion(ev *controlv1.Event) error {
	if ev == nil {
		return fmt.Errorf("%w: no event", contract.ErrUnsupportedSchema)
	}
	version := ev.GetSchemaVersion()
	major, minor, ok := strings.Cut(version, ".")
	switch {
	case !ok || !isDecimal(major) || !isDecimal(minor):
		return fmt.Errorf("%w: event %s: schema_version %s: want MAJOR.MINOR",
			contract.ErrUnsupportedSchema, quoteID(ev.GetEventId()), quoted(version, maxQuotedVersionBytes))
	case major != eventMajor():
		return fmt.Errorf("%w: event %s: schema_version %s: this build reads major %s",
			contract.ErrUnsupportedSchema, quoteID(ev.GetEventId()), quoted(version, maxQuotedVersionBytes), eventMajor())
	}
	return nil
}

// eventMajor reads the major out of the Protobuf package the generated Event
// carries, so a build generated from a v2 contract refuses 1.x.
func eventMajor() string {
	pkg := string((&controlv1.Event{}).ProtoReflect().Descriptor().ParentFile().Package())
	return strings.TrimPrefix(pkg[strings.LastIndex(pkg, ".")+1:], "v")
}

// isDecimal is ParseUint at base 10, which takes no sign, no underscore and no
// empty string, and 32 bits, as pkg/contract reads a version.
func isDecimal(s string) bool {
	_, err := strconv.ParseUint(s, 10, 32)
	return err == nil
}
