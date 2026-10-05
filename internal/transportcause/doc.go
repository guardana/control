// Package transportcause words a transport's failure for a log line by the
// types of the errors it wraps, never by their text: the HTTP client quotes a
// malformed answer's bytes, which can echo the request line and its headers,
// and a protocol library words some failures with the endpoint and the
// session id its peer chose.
package transportcause
