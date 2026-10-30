// Package segmentlog is the RecordLog port, where evidence lines are kept,
// and its default kind, segments: a directory of segment files and a
// manifest that names each one, sealed or pruned.
//
// Appends go to one live segment. At its byte bound the live segment is
// sealed: the manifest names it first, then it is renamed, so a crash between
// the two is completed at the next open. At the log's byte bound an append is
// refused; nothing is pruned automatically. Pruning is an operator's act on
// sealed segments only, and it writes the tombstone before it unlinks.
package segmentlog
