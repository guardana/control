// Package loopback judges an address a local process binds or reaches: it
// has to be host:port with an IP literal on the loopback as its host. A name,
// localhost included, is refused, because what it resolves to is not the
// caller's to vouch for.
package loopback
