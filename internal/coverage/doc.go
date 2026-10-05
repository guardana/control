// Package coverage states, for each path an operator declares, whether a plane
// enforces it, decides it without enforcing, a live source only observed it,
// nothing can tell, or nothing covers it. It reads an inventory, the planes'
// configurations, observation logs and evidence exports, and decides nothing.
//
// It is pure: the caller reads every file and the clock and passes bytes,
// records and the time. See ADR-0041.
package coverage
