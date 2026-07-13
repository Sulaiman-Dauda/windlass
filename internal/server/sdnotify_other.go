//go:build !linux

package server

// NotifySystemd is a no-op off Linux.
func NotifySystemd(state string) {}
