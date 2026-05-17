//go:build !linux && !darwin

package procs

import "context"

// ListByCwdPrefix is a generic fallback for platforms that do not yet
// have an implementation (windows, *bsd, plan9, …). It returns
// ErrUnsupported so the package still builds and links anywhere, and
// callers can branch on errors.Is(err, ErrUnsupported) to degrade.
func ListByCwdPrefix(ctx context.Context, prefix string) ([]Process, error) {
	return nil, ErrUnsupported
}
