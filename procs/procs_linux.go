//go:build linux

package procs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procFSRoot is the procfs mount point. Tests point it at a fake tree
// under t.TempDir().
var procFSRoot = "/proc"

// enumerate walks <procFSRoot>/<pid>/cwd via os.Readlink and reads
// /comm and /cmdline for each pid. Per-pid errors (process exited
// mid-walk, EACCES on another user's process, "cwd" that isn't a
// symlink in a fake tree) are swallowed silently so a single
// unreadable entry does not abort the whole listing.
//
// The context is honored at directory-entry granularity; cancellation
// returns ctx.Err() promptly.
func enumerate(ctx context.Context) ([]Process, error) {
	entries, err := os.ReadDir(procFSRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(entries))
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
			continue
		}
		out = append(out, Process{
			Pid:     pid,
			Cwd:     cwd,
			Command: readComm(filepath.Join(pidDir, "comm")),
			Args:    readCmdline(filepath.Join(pidDir, "cmdline")),
		})
	}
	return out, nil
}

func parsePid(name string) (int, bool) {
	u, err := strconv.ParseUint(name, 10, 32)
	if err != nil {
		return 0, false
	}
	return int(u), true
}

func readComm(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func readCmdline(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}
