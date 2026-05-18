//go:build !linux

package tui

// pidCwdMatches is a no-op on platforms without /proc. The procs
// enumerator on these platforms is itself stubbed (procs/procs_darwin.go
// returns ErrUnsupported), so no PIDs reach this function in practice;
// returning true preserves the historic behavior for any future code
// path that supplies a non-empty list.
func pidCwdMatches(pid int, expected string) bool {
	return true
}
