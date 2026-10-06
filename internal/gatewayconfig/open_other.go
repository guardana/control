//go:build !unix

package gatewayconfig

// openFlags is unused here: files.PermissionBits is false, so the loader
// refuses before it opens anything.
const openFlags = 0
