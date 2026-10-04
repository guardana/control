// Package testreport turns the event stream of `go test -json` into a
// transcript: each passing package's result line; for a failing one, its own
// lines and the output of each test that failed; then one line per skipped test
// and the skip count, so that a skip is visible in the gate's output without
// changing its verdict.
//
// Summarize answers with an error whenever the stream does not prove a pass: no
// event at all, a line that is not an event, a package without a result, a
// failed build, package or test. A failed test fails the run even in a package
// whose result is a pass, as when its TestMain exits 0 after the failure, where
// plain `go test` reports the package ok.
package testreport
