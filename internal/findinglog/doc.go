// Package findinglog keeps the findings log. The log is one JSON Lines file,
// findings.jsonl, in a directory only this account may enter, appended to by
// one writer under an exclusive lock. Each line is one record of the finding
// contract, v1alpha1: a finding, or the report that closes one supervision's
// write. The writer keys every finding on its id and
// verdict with its content digest, so a finding written again is counted as
// a duplicate and one with other content as a conflict, and neither is
// appended. There is no export format. See ADR-0045.
package findinglog
