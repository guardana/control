// Package impact says which documentation pages a change makes suspect. It
// judges a list of changed paths against the covers globs of every page, the
// documentable surfaces and the frozen paths of the docs configuration, and
// reads git's output for the paths a range changed and the commits that
// touched a page's covers since the page itself was last committed.
//
// The lists are computed from values: the walk, the configuration and git
// come in as a file system and a Runner, so every list is tested against a
// synthetic set. Run is the whole program over those seams, and Git the one
// Runner that executes the binary; a git that cannot answer, or answers for
// some other repository, is an explicit unmeasured result, never an empty
// list.
package impact
