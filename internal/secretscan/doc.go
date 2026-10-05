// Package secretscan finds the plane's configured credentials in what an
// upstream answered, in the spellings a server that echoes one is likely to
// use (ADR-0042). It decides nothing about a call; a caller withholds what it
// finds, and what it could not scan.
package secretscan
