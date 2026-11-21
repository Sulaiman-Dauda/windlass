//go:build !unix

package update

// ServiceOwnsBinary is false off Unix, where self-update is not supported.
func ServiceOwnsBinary() bool { return false }
