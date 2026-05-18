// Package procs lists local processes by working-directory prefix. It
// owns nothing more than that: a thin, platform-aware enumeration of
// running processes filtered to those whose cwd lies under a given
// path. The aggregator uses it to attach "processes running in this
// worktree" (especially `claude`) to per-worktree state.
//
// Portability:
//
//   - macOS is first-class. The darwin implementation enumerates pids
//     via the proc_info syscall and reads cwd + argv via
//     proc_pidinfo and KERN_PROCARGS2, with no cgo.
//   - Linux is supported. /proc/<pid>/cwd, /comm, /cmdline.
//   - Other platforms (windows, *bsd, plan9, …) return ErrUnsupported.
//     Callers should treat this as "no process data available" and
//     degrade gracefully, not as a hard failure.
//
// The package has no third-party runtime dependencies beyond
// golang.org/x/sys/unix on darwin. It is safe for concurrent use; both
// list entry points are stateless.
package procs

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// enumerator is the package-level seam tests swap to inject a fake
// process list. Production code leaves it nil and ListByCwdPrefixes
// falls back to the platform-specific enumerate function.
var enumerator func(context.Context) ([]Process, error)

func currentEnumerator() func(context.Context) ([]Process, error) {
	if enumerator != nil {
		return enumerator
	}
	return enumerate
}

// Process is one entry in the result of ListByCwdPrefix /
// ListByCwdPrefixes.
//
// Pid is the kernel pid. Cwd is the absolute path the kernel reports
// for the process's current working directory at the moment of the
// walk; treat it as a snapshot. Command is the executable basename
// (e.g. "claude") — sourced from /proc/<pid>/comm on linux, derived
// from argv[0] basename on darwin. Args is the process command line
// as a slice in argv order; the leading element is conventionally the
// program name.
type Process struct {
	Pid     int
	Cwd     string
	Command string
	Args    []string
}

// ErrUnsupported is returned by the list functions on platforms that
// do not yet have an enumerator (currently: anything other than darwin
// or linux). Callers should match it with errors.Is and treat the
// result as "no data available" rather than a hard error.
var ErrUnsupported = errors.New("procs: platform not supported")

// ListByCwdPrefix returns processes whose cwd has the given prefix.
//
// Bare paths are matched as exact-prefix (so "/work/canopy" matches
// "/work/canopy" and "/work/canopy-feature"); pass a trailing slash to
// require a directory boundary (e.g. "/work/canopy/"). An empty
// prefix matches every process the caller can see.
//
// Per-pid errors during enumeration (process exited mid-walk, EACCES
// on another user's process) are swallowed silently so a single
// unreadable entry does not abort the listing. The result is sorted
// by Pid ascending for determinism, and is always non-nil so callers
// can range over the result unconditionally.
//
// The context is honored at pid granularity; cancellation returns
// ctx.Err() promptly without partial results.
func ListByCwdPrefix(ctx context.Context, prefix string) ([]Process, error) {
	buckets, err := ListByCwdPrefixes(ctx, []string{prefix})
	if err != nil {
		return nil, err
	}
	return buckets[prefix], nil
}

// ListByCwdPrefixes returns processes grouped by which prefix they
// match, using a single enumeration of the process table.
//
// A process whose cwd matches multiple prefixes appears under EACH
// matching prefix. The caller is responsible for any longest-prefix
// deduplication (the aggregator does this against its siblings list).
//
// Bare paths are matched as exact-prefix; pass a trailing slash to
// require a directory boundary. An empty prefix in the slice matches
// every visible process. Prefixes with zero matches map to an empty
// (non-nil) slice. The result map always has one entry per input
// prefix. Ordering within each bucket is Pid ascending.
//
// The context is honored at pid granularity; cancellation returns
// ctx.Err() promptly without partial results.
func ListByCwdPrefixes(ctx context.Context, prefixes []string) (map[string][]Process, error) {
	all, err := currentEnumerator()(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]Process, len(prefixes))
	for _, p := range prefixes {
		out[p] = []Process{}
	}
	for _, proc := range all {
		for _, p := range prefixes {
			if strings.HasPrefix(proc.Cwd, p) {
				out[p] = append(out[p], proc)
			}
		}
	}
	for p := range out {
		sort.Slice(out[p], func(i, j int) bool { return out[p][i].Pid < out[p][j].Pid })
	}
	return out, nil
}
