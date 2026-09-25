// Package gatewayconfig reads the gateway's configuration: a typed table of
// keys with their defaults, environment variables and admitted spellings, a
// strict YAML subset the file is written in, and the loader that binds the
// file and then the environment over it and refuses what it cannot read.
//
// The table is the one place a key is declared. Load, the doctor's settings
// listing and the configuration reference page all derive from it, so a key
// added to the table is loaded, printed and documented without another edit.
package gatewayconfig
