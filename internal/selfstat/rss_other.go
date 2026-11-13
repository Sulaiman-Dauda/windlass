//go:build !linux

package selfstat

// RSS is not measured off Linux, where Windlass does not run in production.
func RSS() (uint64, bool) { return 0, false }
