//go:build !unix

package spool

// noFollow is empty here: the platform keeps no permission bits, so Open
// refuses the directory before any file in it is opened.
const noFollow = 0
