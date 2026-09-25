// Package spool is the evidence sink that writes disk: an append-only,
// checksummed, bounded log of events that an exporter drains and acknowledges
// (ADR-0014). Before an effect a record that cannot be written blocks the call;
// after one it is delivered and the plane stops taking material calls.
package spool
