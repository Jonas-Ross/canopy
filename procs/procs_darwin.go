//go:build darwin

package procs

import "context"

// ListByCwdPrefix is a deliberate stub on macOS. The portable path to
// per-process cwd here is `lsof`, which is privileged and slow; the v1
// scope is Linux-first and the aggregator already treats missing
// process data as a soft degradation. Returning ErrUnsupported lets
// callers branch on errors.Is(err, ErrUnsupported) and hide the
// "processes" column rather than crash. The prefix argument is
// accepted to keep the signature stable across platforms.
func ListByCwdPrefix(ctx context.Context, prefix string) ([]Process, error) {
	return nil, ErrUnsupported
}
