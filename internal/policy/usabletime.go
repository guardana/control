package policy

import "time"

// lastTimestampSecond is 9999-12-31T23:59:59Z, the last second a protobuf
// Timestamp holds.
const lastTimestampSecond = 253402300799

// UsableTime reports whether t can stand for a time the plane confirms,
// decides or expires at: from 1970-01-01T00:00:00Z, before which the zero
// time lies, to the last instant a protobuf Timestamp holds, so a time it
// accepts can always be carried on a record.
func UsableTime(t time.Time) bool {
	return !t.Before(time.Unix(0, 0)) && !t.After(time.Unix(lastTimestampSecond, 999_999_999))
}
