// Package metrics holds the one table of what a plane reports on /metrics,
// and writes it in the Prometheus text exposition format, version 0.0.4, on
// the standard library.
//
// A row names a metric, its type, a line of help and the statistic it reads.
// The reference page is rendered from the same table, and a test walks every
// statistic the plane's seams return, so a field no row reads fails the gate.
// Rendering is a pure function of a Reading: no clock, no I/O.
package metrics
