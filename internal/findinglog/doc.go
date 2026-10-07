// Package findinglog keeps the findings log. The log is one JSON Lines file,
// findings.jsonl, in a directory only this account may enter, appended to by
// one writer under an exclusive lock. Each line is one record of the finding
// contract, v1alpha1, of schema "0.1" or "0.2": a finding, the report that
// closes one supervision's write, or the header a log created by this
// package starts with, holding a random log id. The writer keys every
// finding on its id and verdict with its content digest, so a finding
// written again is counted as a duplicate and one with other content as a
// conflict, and neither is appended. Export reads the committed part of a log
// in ADR-0048's format, a cursor at a time. See ADR-0045, ADR-0047 and
// ADR-0048.
package findinglog
