// Package policywatch keeps a plane's policy current from its bundle file and
// the freshness statement that confirms it: Start installs what the files and
// the serial floor allow, and a Refresher reads both again every interval.
// It reads the files and the clocks and keeps only the verdicts no clock or
// floor can change; what a statement and a floor allow is the policy
// package's to decide.
package policywatch
