//go:build !unix

package main

// ignoreSIGPIPE is a no-op on platforms without SIGPIPE (e.g. Windows), where
// writes to a closed pipe already surface as ordinary errors.
func ignoreSIGPIPE() {}
