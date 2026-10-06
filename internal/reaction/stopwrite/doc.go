// Package stopwrite writes stop lists: it starts one, carries one into a new
// list, and appends a stop, a covered line or a lift, each under an
// exclusive lock and only when the plane's judge would accept the list the
// write makes. The commands link it; a plane never does. See ADR-0046.
package stopwrite
