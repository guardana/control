//go:build !unix

package main

import "testing"

// specialOuts makes no named pipe where the platform has none.
func specialOuts(*testing.T, string) map[string]string { return nil }
