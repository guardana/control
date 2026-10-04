// Package observelog keeps the observation log and writes its export. The log
// is one JSON Lines file, observations.jsonl, in a directory only this
// account may enter, appended to by one writer under an exclusive lock. Each
// line is one observe record: an observation, or the report an import leaves.
// The writer keeps every observation id the file holds with its content
// digest, so an observation read again is counted as a duplicate and one
// resent with other content as a conflict, and neither is appended.
//
// The export is ADR-0035's shape under its own format name, with records of
// type observation and import_report. See ADR-0040.
package observelog
