// Package keytext finds the text of a private key in a message and takes it
// out, so a program can print a setting, a file or a record without
// repeating a key it was handed. It reads no key and imports only the
// standard library, so a binary that must link no key reader can use it.
package keytext
