// Package frontmatter reads and writes the metadata block at the head of a
// documentation page, a strict subset of YAML with seven known keys, and
// measures the words of the body that follows it.
//
// The grammar admits one spelling of each block, so Render is the inverse of
// Parse: a generator writes what the check reads, and a hand-written page
// either parses or names the line that stops it. The package imports only
// the standard library packages the guarded trees may use, because a
// generator inside one of them renders the block too.
package frontmatter
