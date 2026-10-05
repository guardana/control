// Package notify hands each alert of a findings log, in log order, to a
// program the operator names, one JSON line on its standard input, and marks
// it delivered only after the program exits 0. A crash between that exit and
// the mark delivers the one record again on the next run, so delivery is at
// least once and a receiver drops a key it has seen. The state is a directory
// only this account may enter. See ADR-0045.
package notify
