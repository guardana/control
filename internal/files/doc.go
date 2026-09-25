// Package files writes and reads the small files a command or a store keeps on
// disk: a name created once and never replaced, a name replaced whole, and a
// bounded read of a regular file judged by its opened descriptor. A write is
// forced to disk and so is its directory entry, so a name reported written
// survives a power loss.
package files
