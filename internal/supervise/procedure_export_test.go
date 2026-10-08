package supervise

import "reflect"

// SameProcedure reports whether a and b hold the same members, every one a
// procedure keeps to itself included.
func SameProcedure(a, b *Procedure) bool { return reflect.DeepEqual(a, b) }
