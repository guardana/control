// Package observe holds the rules of the observation contract: its version
// table, the source descriptor's validation, the derivation of an
// observation's id and content digest, the restrictive reading of its enums,
// and the codec of one log line.
//
// It is pure: no file, clock, network or randomness, so a later supervisor can
// import it beside the core. See ADR-0040.
package observe
