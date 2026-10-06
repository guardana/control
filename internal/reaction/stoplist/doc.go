// Package stoplist reads a stop list from its file, under the judgement a
// pause file is read under, and serves what a plane decides calls under: the
// snapshot of the last read, judged against the route from the prefix the
// plane accepted before. It reads and never writes; the writer is a package
// a plane does not link. See ADR-0046.
package stoplist
