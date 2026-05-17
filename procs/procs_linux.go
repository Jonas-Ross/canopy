//go:build linux

package procs

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// procFSRoot is the procfs mount point. Indirected through a package
// variable so tests can point at a fake tree built under t.TempDir().
// Production code never reassigns it.
var procFSRoot = "/proc"

// ListByCwdPrefix returns processes whose cwd has the given prefix.
//
// Bare paths are matched as exact-prefix (so "/work/canopy" matches
// "/work/canopy" and "/work/canopy-feature"); pass a trailing slash to
// require a directory boundary (e.g. "/work/canopy/"). An empty prefix
// matches every process the caller can see.
//
// On Linux: walks <procFSRoot>/<pid>/cwd via os.Readlink, filters by
// prefix, then reads /proc/<pid>/comm and /proc/<pid>/cmdline. Per-pid
// errors (process exited mid-walk, EACCES on another user's process)
// are swallowed silently so a single unreadable entry does not abort
// the whole listing. The result is sorted by Pid ascending for
// determinism, and is always non-nil (an empty match returns an empty
// slice, not nil) so callers can range over the result unconditionally.
//
// The context is honored at directory-entry granularity; cancellation
// returns ctx.Err() promptly without partial results.
func ListByCwdPrefix(ctx context.Context, prefix string) ([]Process, error) {
	entries, err := os.ReadDir(procFSRoot)
	if err != nil {
		return nil, err
	}

	out := make([]Process, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		pid, ok := parsePid(name)
		if !ok {
			continue
		}

		pidDir := filepath.Join(procFSRoot, name)

		cwd, err := os.Readlink(filepath.Join(pidDir, "cwd"))
		if err != nil {
			// Process gone, permission denied, or cwd isn't a
			// symlink in the fake tree. Skip silently — a single
			// unreadable pid must not abort the walk.
			continue
		}
		if !strings.HasPrefix(cwd, prefix) {
			continue
		}

		command := readComm(filepath.Join(pidDir, "comm"))
		args := readCmdline(filepath.Join(pidDir, "cmdline"))

		out = append(out, Process{
			Pid:     pid,
			Cwd:     cwd,
			Command: command,
			Args:    args,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Pid < out[j].Pid })
	return out, nil
}

// parsePid returns the integer pid for a /proc subdirectory name, or
// (0, false) if the name isn't all digits. /proc contains many
// non-numeric entries (self, thread-self, meminfo, sys, …) and we want
// to skip them cheaply without leaning on strconv error strings.
func parsePid(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(name)
	if err != nil {
		return 0, false
	}
	return n, true
}

// readComm reads /proc/<pid>/comm. The kernel writes the executable
// basename followed by a single trailing newline; trim it. Read errors
// degrade to an empty string — comm is best-effort metadata, not a
// reason to drop the whole entry.
func readComm(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// readCmdline reads /proc/<pid>/cmdline. The kernel encodes argv with
// NUL separators and (usually) one trailing NUL. An empty cmdline
// (kernel threads, zombies) yields a nil slice rather than [""].
func readCmdline(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	// Trim a single trailing NUL if present, then split. Leaving the
	// trailing NUL in place would produce a spurious empty final
	// element.
	b = []byte(strings.TrimRight(string(b), "\x00"))
	if len(b) == 0 {
		return nil
	}
	parts := strings.Split(string(b), "\x00")
	return parts
}
