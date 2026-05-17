// Package procs lists local processes by working-directory prefix. It
// owns nothing more than that: a thin, platform-aware enumeration of
// running processes filtered to those whose cwd lies under a given
// path. The aggregator uses it to attach "processes running in this
// worktree" (especially `claude`) to per-worktree state.
//
// Portability:
//
//   - Linux is first-class. The implementation walks /proc/<pid>/cwd
//     via os.Readlink plus /proc/<pid>/comm and /proc/<pid>/cmdline.
//   - macOS and other platforms return ErrUnsupported. Callers should
//     treat this as "no process data available" and degrade gracefully,
//     not as a hard failure.
//
// The package has no third-party dependencies and shells out to
// nothing. It is safe for concurrent use; ListByCwdPrefix is stateless.
package procs

import "errors"

// Process is one entry in the result of ListByCwdPrefix.
//
// Pid is the kernel pid. Cwd is the absolute path the kernel reports
// for the process's current working directory at the moment of the
// walk; treat it as a snapshot. Command is the executable basename
// (e.g. "claude"), sourced from /proc/<pid>/comm on Linux. Args is the
// process command line as a slice in argv order, sourced from
// /proc/<pid>/cmdline on Linux; the leading element is conventionally
// the program name.
type Process struct {
	Pid     int
	Cwd     string
	Command string
	Args    []string
}

// ErrUnsupported is returned by ListByCwdPrefix on platforms that do
// not yet have an implementation (currently: anything but Linux).
// Callers should match it with errors.Is and treat the result as "no
// data available" rather than a hard error.
var ErrUnsupported = errors.New("procs: platform not supported")
