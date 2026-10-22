//go:build !unix

package main

// lockDataDir is a no-op where flock is unavailable. Windlass is only
// installed on Linux; this keeps `go build` working elsewhere.
func lockDataDir(string) (func(), error) { return func() {}, nil }
