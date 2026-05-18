//go:build darwin

package procs

import "context"

// enumerate is a stub on darwin; replaced in Task 3+ with a real
// implementation. Returning ErrUnsupported lets callers branch on
// errors.Is(err, ErrUnsupported) and hide procs columns until the
// real impl lands.
func enumerate(_ context.Context) ([]Process, error) {
	return nil, ErrUnsupported
}
