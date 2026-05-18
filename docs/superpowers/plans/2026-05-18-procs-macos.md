# Procs macOS + batch API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the macOS `procs` stub with a pure-Go raw-syscall implementation and switch the aggregator to a batched single-walk API.

**Architecture:** Refactor `procs/` so each platform file exposes only `enumerate(ctx) ([]Process, error)` and cross-platform `procs.go` owns prefix filtering. Add a new public `ListByCwdPrefixes(prefixes []string)` that returns one full walk bucketed per prefix. Implement darwin `enumerate` via `proc_listpids` + `proc_pidinfo(PROC_PIDVNODEPATHINFO)` + `sysctl(KERN_PROCARGS2)` through `golang.org/x/sys/unix`. Wire the aggregator's `refreshAll`/`Snapshot` to call the batch API exactly once per refresh, passing the result down to `buildState`.

**Tech Stack:** Go 1.24, `golang.org/x/sys/unix` (already indirect), Bubble Tea TUI consumer. No cgo. Pure-Go binaries on every platform.

**Spec:** `docs/superpowers/specs/2026-05-18-procs-macos-design.md`

**Branch:** `feat/procs-macos` (worktree at `.claude/worktrees/feat+procs-macos`)

---

## File Structure

### Files to create
- `procs/procs_test.go` — cross-platform tests for `ListByCwdPrefix`, `ListByCwdPrefixes` bucketing, against an injected `enumerate` stub.
- `procs/procs_darwin_test.go` — darwin smoke test that calls real `systemEnumerate` and finds the test binary's own pid.

### Files to modify
- `procs/procs.go` — gains `ListByCwdPrefixes`; cross-platform filter logic lives here; defines `var enumerate` interface point.
- `procs/procs_linux.go` — shrinks to one exported func `enumerate` + helpers. Filtering moves out.
- `procs/procs_linux_test.go` — adjust to point tests at `enumerate` rather than `ListByCwdPrefix` (filtering is now covered cross-platform).
- `procs/procs_darwin.go` — real implementation: `var enumerate = systemEnumerate` + the syscall body.
- `procs/procs_other.go` — `func enumerate(ctx) ([]Process, error) { return nil, ErrUnsupported }`.
- `procs/procs_stub_test.go` — narrow build tag to `!linux && !darwin`.
- `aggregator/types.go` — replace `listProcs` field with `listProcsByPrefixes`.
- `aggregator/aggregator.go` — `withDefaults` wires the new field; `Snapshot`/`walkAll`/`buildState` thread a procs snapshot through.
- `aggregator/loop.go` — `refreshAll` calls procs once before per-worktree builds; `refreshOne` calls with a one-element slice.
- `aggregator/aggregator_test.go` — `fakeSources` gains a per-prefix-map `listProcsByPrefixes` and a call counter; existing scenarios adapt their seam wiring.
- `aggregator/diff.go` — no behaviour change, but verify `procsEqual` still works (it does; same slice shape).
- `go.mod` — promote `golang.org/x/sys` from indirect to direct.
- `CLAUDE.md` — flip "Linux-first" line 58 and hard-rule line 73.
- `docs/handoff.md` — line 121 ("primarily on Linux/WSL2").

### Files NOT touched
- `tui/` — `Procs` field shape unchanged; columns keep rendering.
- `cmd/demo/` — sandbox uses procs transitively; no API change reaches it.
- `sessions/`, `git/`, `pr/` — untouched.

---

## Task 1: Extract `enumerate` seam — preserve linux behavior

Refactor `procs_linux.go` so it owns *only* enumeration; cross-platform `procs.go` owns filtering. No behavior change. Existing tests pass unchanged after a one-line shim adjustment.

**Files:**
- Modify: `procs/procs.go`
- Modify: `procs/procs_linux.go`

- [ ] **Step 1.1: Read both files top to bottom**

Run: `cat procs/procs.go procs/procs_linux.go`

Internalize the current `ListByCwdPrefix` body — you'll move ~half of it into `procs.go`.

- [ ] **Step 1.2: Rewrite `procs/procs.go` to own filtering**

Replace the contents of `procs/procs.go` with:

```go
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
	all, err := enumerate(ctx)
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
```

- [ ] **Step 1.3: Rewrite `procs/procs_linux.go` to expose only `enumerate`**

Replace the contents of `procs/procs_linux.go` with:

```go
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
```

- [ ] **Step 1.4: Update `procs/procs_other.go` to expose `enumerate`**

Replace the contents of `procs/procs_other.go` with:

```go
//go:build !linux && !darwin

package procs

import "context"

func enumerate(_ context.Context) ([]Process, error) {
	return nil, ErrUnsupported
}
```

- [ ] **Step 1.5: Build & test on the linux test path (use a linux CI run or local Docker if on mac)**

If you're on darwin and don't have a linux build env, skip ahead — the linux tests will run in CI. Lock in correctness on darwin via Step 1.6.

Otherwise: `GOOS=linux go test ./procs/ -race`
Expected: PASS for the existing `procs_linux_test.go` test set (with the test seam still pointed at `procFSRoot`).

- [ ] **Step 1.6: Verify the package still builds on darwin**

Run: `go build ./procs/...`
Expected: builds clean. The darwin stub still returns `ErrUnsupported` because we haven't replaced it yet (Task 3+).

- [ ] **Step 1.7: Commit**

```bash
git add procs/procs.go procs/procs_linux.go procs/procs_other.go
git commit -m "refactor(procs): extract enumerate seam, filter centrally"
```

---

## Task 2: Add `ListByCwdPrefixes` cross-platform tests

Add cross-platform tests that exercise the bucketing logic against an injected `enumerate` stub. These tests run on every OS — no build tag.

**Files:**
- Create: `procs/procs_test.go`
- Modify: `procs/procs.go` (turn `enumerate` into a `var` so tests can swap it)

- [ ] **Step 2.1: Make `enumerate` swappable**

In `procs/procs.go`, the package-level entry point currently dispatches to a same-named function in each build-tagged file. To let cross-platform tests inject a fake, we need a swappable seam. The cleanest pattern: keep the platform-specific functions named `enumerate` (as today) and add ONE indirection in `procs.go`.

Add to `procs/procs.go` near the top of the file, right after the imports:

```go
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
```

Then in `ListByCwdPrefixes`, change:

```go
all, err := enumerate(ctx)
```

to:

```go
all, err := currentEnumerator()(ctx)
```

- [ ] **Step 2.2: Write failing tests first**

Create `procs/procs_test.go`:

```go
package procs

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func withFakeEnumerator(t *testing.T, ps []Process, err error) {
	t.Helper()
	orig := enumerator
	enumerator = func(ctx context.Context) ([]Process, error) {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if err != nil {
			return nil, err
		}
		return append([]Process(nil), ps...), nil
	}
	t.Cleanup(func() { enumerator = orig })
}

func TestListByCwdPrefixes_BucketsPerPrefix(t *testing.T) {
	withFakeEnumerator(t, []Process{
		{Pid: 1, Cwd: "/repo"},
		{Pid: 2, Cwd: "/repo/.worktrees/feat"},
		{Pid: 3, Cwd: "/elsewhere"},
	}, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{"/repo", "/repo/.worktrees/feat"})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}

	// /repo matches pids 1 AND 2 (both prefixes are valid for pid 2).
	if got1 := pidsOf(got["/repo"]); !reflect.DeepEqual(got1, []int{1, 2}) {
		t.Errorf("[/repo] pids = %v, want [1 2]", got1)
	}
	// /repo/.worktrees/feat matches only pid 2.
	if got2 := pidsOf(got["/repo/.worktrees/feat"]); !reflect.DeepEqual(got2, []int{2}) {
		t.Errorf("[/repo/.worktrees/feat] pids = %v, want [2]", got2)
	}
}

func TestListByCwdPrefixes_EmptyPrefixMatchesAll(t *testing.T) {
	withFakeEnumerator(t, []Process{
		{Pid: 7, Cwd: "/a"},
		{Pid: 8, Cwd: "/b"},
	}, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{""})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}
	if pids := pidsOf(got[""]); !reflect.DeepEqual(pids, []int{7, 8}) {
		t.Errorf("[\"\"] pids = %v, want [7 8]", pids)
	}
}

func TestListByCwdPrefixes_NoMatches_EmptyNonNil(t *testing.T) {
	withFakeEnumerator(t, []Process{{Pid: 1, Cwd: "/x"}}, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{"/missing"})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}
	bucket, ok := got["/missing"]
	if !ok {
		t.Fatalf("missing bucket for input prefix")
	}
	if bucket == nil {
		t.Fatalf("want empty non-nil slice, got nil")
	}
	if len(bucket) != 0 {
		t.Fatalf("want empty slice, got %+v", bucket)
	}
}

func TestListByCwdPrefixes_OneBucketPerInputPrefix(t *testing.T) {
	withFakeEnumerator(t, nil, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{"/a", "/b", "/c"})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 buckets, got %d: %v", len(got), got)
	}
	for _, p := range []string{"/a", "/b", "/c"} {
		if _, ok := got[p]; !ok {
			t.Errorf("missing bucket for prefix %q", p)
		}
	}
}

func TestListByCwdPrefixes_TrailingSlashBoundary(t *testing.T) {
	withFakeEnumerator(t, []Process{
		{Pid: 1, Cwd: "/repo"},
		{Pid: 2, Cwd: "/repo-other"},
		{Pid: 3, Cwd: "/repo/sub"},
	}, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{"/repo/"})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}
	if pids := pidsOf(got["/repo/"]); !reflect.DeepEqual(pids, []int{3}) {
		t.Errorf("want only pid 3 inside /repo/, got %v", pids)
	}
}

func TestListByCwdPrefixes_SortedByPid(t *testing.T) {
	withFakeEnumerator(t, []Process{
		{Pid: 99, Cwd: "/x"},
		{Pid: 5, Cwd: "/x"},
		{Pid: 42, Cwd: "/x"},
	}, nil)

	got, err := ListByCwdPrefixes(context.Background(), []string{"/x"})
	if err != nil {
		t.Fatalf("ListByCwdPrefixes: %v", err)
	}
	if pids := pidsOf(got["/x"]); !reflect.DeepEqual(pids, []int{5, 42, 99}) {
		t.Errorf("want sorted [5 42 99], got %v", pids)
	}
}

func TestListByCwdPrefixes_ContextCancelled(t *testing.T) {
	withFakeEnumerator(t, []Process{{Pid: 1, Cwd: "/a"}}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ListByCwdPrefixes(ctx, []string{"/a"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestListByCwdPrefix_SinglePrefixDelegatesCorrectly(t *testing.T) {
	withFakeEnumerator(t, []Process{
		{Pid: 1, Cwd: "/match"},
		{Pid: 2, Cwd: "/other"},
	}, nil)

	got, err := ListByCwdPrefix(context.Background(), "/match")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if pids := pidsOf(got); !reflect.DeepEqual(pids, []int{1}) {
		t.Errorf("want [1], got %v", pids)
	}
}

func pidsOf(ps []Process) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.Pid
	}
	return out
}
```

- [ ] **Step 2.3: Run tests, verify they pass**

Run: `go test ./procs/ -run ListByCwdPrefixes -v`
Expected: PASS — all 7 tests + the delegate test green.

- [ ] **Step 2.4: Commit**

```bash
git add procs/procs.go procs/procs_test.go
git commit -m "feat(procs): add ListByCwdPrefixes batch API"
```

---

## Task 3: Darwin — pid enumeration via proc_listpids

Implement the first half of darwin enumeration: get a complete pid list. This is the only call whose failure is non-recoverable (matches `os.ReadDir("/proc")` on linux).

**Files:**
- Modify: `procs/procs_darwin.go`

- [ ] **Step 3.1: Replace `procs_darwin.go` with a real skeleton**

Replace the entire contents of `procs/procs_darwin.go` with:

```go
//go:build darwin

package procs

import (
	"context"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin proc_info syscall arguments. See xnu's bsd/sys/proc_info.h.
const (
	// callnum values for SYS_PROC_INFO.
	procInfoCallListPIDs = 1
	procInfoCallPIDInfo  = 2

	// proc_listpids "type" argument.
	procAllPIDs = 1

	// proc_pidinfo "flavor" values.
	procPIDVNodePathInfo = 9
)

// systemEnumerate walks the live process table via the proc_info
// syscall plus KERN_PROCARGS2 for argv. Per-pid errors are swallowed
// so a single transient pid (exited between listing and reading) does
// not abort the walk.
func systemEnumerate(ctx context.Context) ([]Process, error) {
	pids, err := listAllPIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid == 0 {
			continue
		}
		cwd, ok := readCWD(pid)
		if !ok {
			continue
		}
		args := readArgv(pid)
		out = append(out, Process{
			Pid:     int(pid),
			Cwd:     cwd,
			Command: commandFromArgs(args),
			Args:    args,
		})
	}
	return out, nil
}

// listAllPIDs returns every visible pid using proc_listpids with
// PROC_ALL_PIDS. Performs a size-probe call first, then a sized read.
// We double the probe result to absorb forks between calls.
func listAllPIDs() ([]int32, error) {
	probe, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallListPIDs,
		procAllPIDs,
		0,
		0,
		0, 0,
	)
	if errno != 0 {
		return nil, errno
	}
	if probe == 0 {
		return nil, nil
	}

	bufBytes := int(probe) * 2
	buf := make([]byte, bufBytes)
	n, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallListPIDs,
		procAllPIDs,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(bufBytes),
		0,
	)
	if errno != 0 {
		return nil, errno
	}

	const sz = int(unsafe.Sizeof(int32(0)))
	count := int(n) / sz
	pids := make([]int32, count)
	for i := range pids {
		pids[i] = *(*int32)(unsafe.Pointer(&buf[i*sz]))
	}
	return pids, nil
}

// readCWD is implemented in Task 4.
func readCWD(pid int32) (string, bool) {
	_ = pid
	return "", false
}

// readArgv is implemented in Task 5.
func readArgv(pid int32) []string {
	_ = pid
	return nil
}

// commandFromArgs returns Args[0] basename, or "" if Args is empty.
func commandFromArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	a := args[0]
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == '/' {
			return a[i+1:]
		}
	}
	return a
}

// enumerate is the seam picked up by ListByCwdPrefixes when the
// `enumerator` var is unset.
func enumerate(ctx context.Context) ([]Process, error) {
	return systemEnumerate(ctx)
}
```

- [ ] **Step 3.2: Promote x/sys to a direct dep, tidy, verify build**

Run: `go get golang.org/x/sys/unix && go mod tidy && go build ./...`
Expected: build clean. `go.mod` shows `golang.org/x/sys` as a direct (no `// indirect`).

- [ ] **Step 3.3: Commit**

```bash
git add procs/procs_darwin.go go.mod go.sum
git commit -m "feat(procs): darwin pid enumeration via proc_listpids"
```

---

## Task 4: Darwin — per-pid cwd via proc_pidinfo

Implement `readCWD(pid)` against `PROC_PIDVNODEPATHINFO`. This is the data that makes Canopy's worktree filtering possible.

**Files:**
- Modify: `procs/procs_darwin.go`

- [ ] **Step 4.1: Replace `readCWD` stub**

Replace the `readCWD` stub in `procs/procs_darwin.go` with:

```go
// procVNodePathInfoSize is sizeof(struct proc_vnodepathinfo) from
// xnu's bsd/sys/proc_info.h. Two proc_vnodeinfo_path entries (cdir,
// rdir), each containing a proc_vnodeinfo (152 bytes) and a
// proc_vnodepath (1024 bytes path + flags). Total = 2 * 1176 = 2352.
// Verified against xnu-* headers; pinned here so a Go-side struct
// definition drift can't surface as a silent buffer overrun.
const procVNodePathInfoSize = 2352

// cdirPathOffset is the byte offset of cdir.vip_path inside the
// proc_vnodepathinfo struct. cdir is the first proc_vnodeinfo_path
// entry; vip_path is preceded by the 152-byte vip_vi (proc_vnodeinfo).
const cdirPathOffset = 152

// maxPathLen mirrors MAXPATHLEN (PATH_MAX) on darwin.
const maxPathLen = 1024

// readCWD returns the cwd for a pid via proc_pidinfo. Returns false on
// any syscall error (ESRCH if the pid exited, EPERM for processes
// outside the caller's uid). Errors are silent — a transient pid must
// not abort the walk.
func readCWD(pid int32) (string, bool) {
	var buf [procVNodePathInfoSize]byte
	n, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		procInfoCallPIDInfo,
		uintptr(pid),
		procPIDVNodePathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if errno != 0 {
		return "", false
	}
	if int(n) < cdirPathOffset+maxPathLen {
		return "", false
	}
	path := buf[cdirPathOffset : cdirPathOffset+maxPathLen]
	end := 0
	for end < len(path) && path[end] != 0 {
		end++
	}
	if end == 0 {
		return "", false
	}
	return string(path[:end]), true
}
```

- [ ] **Step 4.2: Verify build**

Run: `go build ./...`
Expected: build clean.

- [ ] **Step 4.3: Smoke test it manually (darwin host only)**

This is a one-off sanity check, not a saved test. Run:

```bash
cat > /tmp/procs_smoke.go <<'EOF'
package main

import (
	"context"
	"fmt"

	"github.com/jonasross/canopy/procs"
)

func main() {
	ps, err := procs.ListByCwdPrefix(context.Background(), "")
	if err != nil {
		panic(err)
	}
	for i, p := range ps {
		if i >= 5 {
			fmt.Printf("... (%d total)\n", len(ps))
			break
		}
		fmt.Printf("pid=%d cwd=%q\n", p.Pid, p.Cwd)
	}
}
EOF
go run /tmp/procs_smoke.go
rm /tmp/procs_smoke.go
```

Expected: prints 5 lines of `pid=N cwd="/some/path"` and a total count. Cwds should be real paths, not empty.

If cwd is consistently empty, `procVNodePathInfoSize` / `cdirPathOffset` may differ on your darwin version — recheck against `bsd/sys/proc_info.h` in the matching xnu source tag, fix the constants, re-test.

- [ ] **Step 4.4: Commit**

```bash
git add procs/procs_darwin.go
git commit -m "feat(procs): darwin per-pid cwd via PROC_PIDVNODEPATHINFO"
```

---

## Task 5: Darwin — argv via KERN_PROCARGS2

Implement `readArgv(pid)` against `sysctl(KERN_PROCARGS2)`. This populates `Args`, and `Command` derives from `Args[0]`.

**Files:**
- Modify: `procs/procs_darwin.go`

- [ ] **Step 5.1: Replace `readArgv` stub**

Replace the `readArgv` stub in `procs/procs_darwin.go` with:

```go
// readArgv returns the argv vector of a pid via sysctl KERN_PROCARGS2.
// The kernel returns a packed buffer:
//
//   int32 argc
//   char  exec_path[]   // null-terminated, zero-padded
//   char  argv[argc][]  // each null-terminated
//   char  envp[]        // ignored
//
// Returns nil on any sysctl error (transient pid, permission denied,
// short buffer). nil is the same shape Linux returns for kernel
// threads, so callers treat both identically.
func readArgv(pid int32) []string {
	mib := []int32{unix.CTL_KERN, unix.KERN_PROCARGS2, pid}
	buf, err := unix.SysctlRaw("kern.procargs2", int(pid))
	_ = mib // SysctlRaw composes the mib internally for "kern.procargs2"; mib kept for documentation.
	if err != nil || len(buf) < 4 {
		return nil
	}

	argc := *(*int32)(unsafe.Pointer(&buf[0]))
	if argc <= 0 {
		return nil
	}

	// Skip the int32 argc header.
	p := buf[4:]

	// Skip exec_path: the first null-terminated string.
	i := 0
	for i < len(p) && p[i] != 0 {
		i++
	}
	if i >= len(p) {
		return nil
	}
	// Skip the null terminator AND any zero padding that follows.
	for i < len(p) && p[i] == 0 {
		i++
	}
	p = p[i:]

	// Read argc null-terminated strings.
	args := make([]string, 0, argc)
	for len(args) < int(argc) && len(p) > 0 {
		end := 0
		for end < len(p) && p[end] != 0 {
			end++
		}
		args = append(args, string(p[:end]))
		if end >= len(p) {
			break
		}
		p = p[end+1:]
	}
	if len(args) == 0 {
		return nil
	}
	return args
}
```

(`unix.SysctlRaw("kern.procargs2", int(pid))` accepts named-form sysctls; on darwin it resolves to `{CTL_KERN, KERN_PROCARGS2, pid}` internally. The `mib` slice in the body is kept as a doc-only reference for future readers.)

- [ ] **Step 5.2: Drop the unused `mib` slice**

The `_ = mib` line is a code-smell. Remove the `mib` declaration and the `_ = mib` line entirely; the comment block above the function already documents the mib layout.

After cleanup, the function head reads:

```go
func readArgv(pid int32) []string {
	buf, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil || len(buf) < 4 {
		return nil
	}
	...
}
```

- [ ] **Step 5.3: Verify build**

Run: `go build ./...`
Expected: build clean.

- [ ] **Step 5.4: Smoke test (darwin host)**

```bash
cat > /tmp/procs_argv_smoke.go <<'EOF'
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jonasross/canopy/procs"
)

func main() {
	ps, err := procs.ListByCwdPrefix(context.Background(), "")
	if err != nil {
		panic(err)
	}
	self := os.Getpid()
	for _, p := range ps {
		if p.Pid == self {
			fmt.Printf("self: cmd=%q args=%v\n", p.Command, p.Args)
			return
		}
	}
	fmt.Println("self pid not found")
}
EOF
go run /tmp/procs_argv_smoke.go
rm /tmp/procs_argv_smoke.go
```

Expected: `self: cmd="go" args=[...]` (or similar — the test binary's argv).

- [ ] **Step 5.5: Commit**

```bash
git add procs/procs_darwin.go
git commit -m "feat(procs): darwin argv via KERN_PROCARGS2"
```

---

## Task 6: Darwin test seam + smoke test

Wire the darwin file into the test seam (already swappable via the cross-platform `enumerator` var) and add a single smoke test that runs the real `systemEnumerate` on the host. Drop the darwin branch from `procs_stub_test.go`.

**Files:**
- Create: `procs/procs_darwin_test.go`
- Modify: `procs/procs_stub_test.go`

- [ ] **Step 6.1: Narrow `procs_stub_test.go` to `!linux && !darwin`**

In `procs/procs_stub_test.go`, change the build tag from:

```go
//go:build !linux
```

to:

```go
//go:build !linux && !darwin
```

- [ ] **Step 6.2: Create the darwin smoke test**

Create `procs/procs_darwin_test.go`:

```go
//go:build darwin

package procs

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestSystemEnumerate_FindsSelf confirms the real darwin syscall path
// finds the test process and populates Cwd + Args. This is the only
// test that touches actual syscalls — the bucketing/filter logic is
// covered cross-platform in procs_test.go via the enumerator seam.
func TestSystemEnumerate_FindsSelf(t *testing.T) {
	got, err := systemEnumerate(context.Background())
	if err != nil {
		t.Fatalf("systemEnumerate: %v", err)
	}
	self := os.Getpid()
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for _, p := range got {
		if p.Pid != self {
			continue
		}
		if p.Cwd != wantCwd {
			t.Errorf("Cwd = %q, want %q", p.Cwd, wantCwd)
		}
		if len(p.Args) == 0 {
			t.Errorf("Args empty, want at least argv[0]")
		}
		if p.Command == "" {
			t.Errorf("Command empty, want non-empty")
		}
		if !strings.Contains(p.Args[0], p.Command) {
			t.Errorf("Args[0]=%q does not contain Command=%q", p.Args[0], p.Command)
		}
		return
	}
	t.Fatalf("self pid %d not found in %d enumerated processes", self, len(got))
}

func TestSystemEnumerate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := systemEnumerate(ctx)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
```

- [ ] **Step 6.3: Run the darwin tests**

Run: `go test ./procs/ -race -v`
Expected: all cross-platform tests PASS, plus `TestSystemEnumerate_FindsSelf` and `TestSystemEnumerate_ContextCancelled` PASS.

If `TestSystemEnumerate_FindsSelf` fails on Cwd assertion: the constants in Task 4 may be wrong for your darwin SDK version. Recheck `procVNodePathInfoSize` against your SDK's `bsd/sys/proc_info.h`.

- [ ] **Step 6.4: Commit**

```bash
git add procs/procs_darwin_test.go procs/procs_stub_test.go
git commit -m "test(procs): darwin smoke test for systemEnumerate"
```

---

## Task 7: Aggregator — batch one walk per refresh

Switch the aggregator from N per-worktree calls to one batched call per refresh cycle. The test seam in `Config` changes shape; all test scenarios adapt.

**Files:**
- Modify: `aggregator/types.go`
- Modify: `aggregator/aggregator.go`
- Modify: `aggregator/loop.go`
- Modify: `aggregator/aggregator_test.go`

- [ ] **Step 7.1: Read the current aggregator integration to lock in your mental model**

Run: `grep -n "listProcs\|buildState\|cfg.listProcs" aggregator/*.go`

Note the call sites:
- `aggregator/aggregator.go:70` — `withDefaults` wiring.
- `aggregator/aggregator.go:152` — `buildState` call.
- `aggregator/loop.go:172` and `aggregator/loop.go:228` — `refreshAll` and `refreshOne` callers of `buildState`.
- `aggregator/aggregator.go:93` — `Snapshot` calls `buildState` through `walkAll`.

- [ ] **Step 7.2: Change the Config seam to the batch shape**

In `aggregator/types.go`, replace this line:

```go
	listProcs      func(ctx context.Context, prefix string) ([]procs.Process, error)
```

with:

```go
	listProcsByPrefixes func(ctx context.Context, prefixes []string) (map[string][]procs.Process, error)
```

- [ ] **Step 7.3: Update `withDefaults`**

In `aggregator/aggregator.go`, replace:

```go
	if cfg.listProcs == nil {
		cfg.listProcs = procs.ListByCwdPrefix
	}
```

with:

```go
	if cfg.listProcsByPrefixes == nil {
		cfg.listProcsByPrefixes = procs.ListByCwdPrefixes
	}
```

- [ ] **Step 7.4: Thread the snapshot through `buildState`**

`buildState` currently calls `a.cfg.listProcs(ctx, state.Worktree.Path)` itself. Refactor so the caller hands it a pre-bucketed slice for this worktree.

In `aggregator/aggregator.go`, change the `buildState` signature from:

```go
func (a *Aggregator) buildState(ctx context.Context, repo Repo, wt git.Worktree, siblings []string, prList []pr.PR, prStale bool) WorktreeState {
```

to:

```go
func (a *Aggregator) buildState(ctx context.Context, repo Repo, wt git.Worktree, siblings []string, prList []pr.PR, prStale bool, procsByPrefix map[string][]procs.Process) WorktreeState {
```

Inside the body, replace:

```go
	if ps, err := a.cfg.listProcs(ctx, state.Worktree.Path); err == nil && ps != nil {
		state.Procs = ps[:0]
		for _, p := range ps {
			if longestMatchingPath(p.Cwd, siblings) == state.Worktree.Path {
				state.Procs = append(state.Procs, p)
			}
		}
	}
```

with:

```go
	if ps, ok := procsByPrefix[state.Worktree.Path]; ok {
		state.Procs = ps[:0]
		for _, p := range ps {
			if longestMatchingPath(p.Cwd, siblings) == state.Worktree.Path {
				state.Procs = append(state.Procs, p)
			}
		}
	}
```

- [ ] **Step 7.5: Add a `procsSnapshot` helper**

In `aggregator/aggregator.go`, add a small helper right above `buildState`:

```go
// procsSnapshot is a one-shot batched procs walk across every prefix
// the caller cares about. Failures are soft-degraded: an error returns
// an empty map so buildState's bucket lookup simply finds no entry
// and leaves Procs as the zero slice. Same shape as the per-pid
// silent-skip behavior in procs/.
func (a *Aggregator) procsSnapshot(ctx context.Context, prefixes []string) map[string][]procs.Process {
	if len(prefixes) == 0 {
		return map[string][]procs.Process{}
	}
	m, err := a.cfg.listProcsByPrefixes(ctx, prefixes)
	if err != nil {
		return map[string][]procs.Process{}
	}
	return m
}
```

- [ ] **Step 7.6: Wire `Snapshot` to call procs once**

In `aggregator/aggregator.go`, change `Snapshot`:

```go
func (a *Aggregator) Snapshot(ctx context.Context) ([]WorktreeState, error) {
	prefixes := a.collectPrefixes(ctx)
	pbp := a.procsSnapshot(ctx, prefixes)
	var out []WorktreeState
	err := a.walkAll(ctx, func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool) {
		out = append(out, a.buildState(ctx, repo, wt, siblings, prs, prStale, pbp))
	}, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}
```

And add `collectPrefixes` as a sibling helper (right above `procsSnapshot`):

```go
// collectPrefixes enumerates every worktree path across configured
// repos so a single batched procs call covers them all. Errors during
// list-worktrees fall through as a missing entry; the corresponding
// worktree just won't have its prefix in the bucket map, and
// buildState will leave Procs empty for that one.
func (a *Aggregator) collectPrefixes(ctx context.Context) []string {
	out := make([]string, 0)
	for _, repo := range a.cfg.Repos {
		wts, err := a.cfg.listWorktrees(ctx, repo.Root)
		if err != nil {
			continue
		}
		for _, wt := range wts {
			out = append(out, wt.Path)
		}
	}
	return out
}
```

- [ ] **Step 7.7: Wire `refreshAll` to call procs once**

In `aggregator/loop.go`, change `refreshAll`'s body. Replace this:

```go
	seen := make(map[string]struct{})
	_ = a.walkAll(ctx,
		func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool) {
			seen[wt.Path] = struct{}{}
			pathToRepo[wt.Path] = repo
			next := a.buildState(ctx, repo, wt, siblings, prs, prStale)
```

with:

```go
	pbp := a.procsSnapshot(ctx, a.collectPrefixes(ctx))
	seen := make(map[string]struct{})
	_ = a.walkAll(ctx,
		func(repo Repo, wt git.Worktree, siblings []string, prs []pr.PR, prStale bool) {
			seen[wt.Path] = struct{}{}
			pathToRepo[wt.Path] = repo
			next := a.buildState(ctx, repo, wt, siblings, prs, prStale, pbp)
```

- [ ] **Step 7.8: Wire `refreshOne` to call procs with a one-element slice**

In `aggregator/loop.go`, find the `refreshOne` body. Replace:

```go
	next := a.buildState(ctx, repo, full, siblings, prList, prStale)
```

with:

```go
	pbp := a.procsSnapshot(ctx, []string{path})
	next := a.buildState(ctx, repo, full, siblings, prList, prStale, pbp)
```

- [ ] **Step 7.9: Update the test fakes**

In `aggregator/aggregator_test.go`, find `fakeSources.listProcs`:

```go
func (f *fakeSources) listProcs(ctx context.Context, prefix string) ([]procs.Process, error) {
	if f.procsByPrefix != nil {
		return f.procsByPrefix(prefix)
	}
	if f.procsErr != nil {
		return nil, f.procsErr
	}
	return append([]procs.Process(nil), f.procs[prefix]...), nil
}
```

Replace with:

```go
func (f *fakeSources) listProcsByPrefixes(ctx context.Context, prefixes []string) (map[string][]procs.Process, error) {
	if f.procsCalls != nil {
		f.procsCalls.Add(1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.procsErr != nil {
		return nil, f.procsErr
	}
	out := make(map[string][]procs.Process, len(prefixes))
	for _, p := range prefixes {
		if f.procsByPrefix != nil {
			ps, err := f.procsByPrefix(p)
			if err != nil {
				return nil, err
			}
			out[p] = ps
			continue
		}
		out[p] = append([]procs.Process(nil), f.procs[p]...)
	}
	return out, nil
}
```

Add a new field to the `fakeSources` struct, alongside the existing optional counters:

```go
	procsCalls   *atomic.Int64 // optional counter; tests provide it to assert invocation
```

(The existing `listWtCalls` / `statusCalls` follow the same pointer-optional pattern; `sync/atomic` is already imported.)

Then update every Config-construction site in `aggregator_test.go` that says:

```go
		listProcs:      fakes.listProcs,
```

to say:

```go
		listProcsByPrefixes: fakes.listProcsByPrefixes,
```

- [ ] **Step 7.10: Add a one-call invariant test**

Append to `aggregator/aggregator_test.go`. This test follows the same construction pattern as the surrounding tests: `&fakeSources{...}` literal, `newTestAggregator` helper (from `aggregator/helpers_test.go`), and a `Repo` slice.

```go
// TestRefreshAll_CallsProcsOnce ensures the batched procs walk fires
// exactly once per Snapshot, not once per worktree. Regression check
// for the N-walks-per-refresh waste fixed in the macOS port.
func TestRefreshAll_CallsProcsOnce(t *testing.T) {
	const repoRoot = "/repo"
	wt1 := "/repo"
	wt2 := "/repo/.wt/feat-a"
	wt3 := "/repo/.wt/feat-b"

	procsCalls := &atomic.Int64{}
	fakes := &fakeSources{
		worktrees: map[string][]git.Worktree{
			repoRoot: {
				{Path: wt1, Branch: "main"},
				{Path: wt2, Branch: "feat-a"},
				{Path: wt3, Branch: "feat-b"},
			},
		},
		procsCalls: procsCalls,
	}

	store := openTestSessionStore(t, func(string) {})

	a := newTestAggregator(t, Config{
		Repos:               []Repo{{Root: repoRoot, Name: "repo"}},
		SessionStore:        store,
		listWorktrees:       fakes.listWorktrees,
		worktreeStatus:      fakes.worktreeStatus,
		listProcsByPrefixes: fakes.listProcsByPrefixes,
	})

	if _, err := a.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := procsCalls.Load(); got != 1 {
		t.Errorf("listProcsByPrefixes calls = %d, want 1", got)
	}
}
```

Three worktrees in one repo. Before the batching change, this would have produced 3 `listProcs` calls; now it must produce exactly 1.

- [ ] **Step 7.11: Run all aggregator tests**

Run: `go test ./aggregator/ -race -v`
Expected: PASS. If any pre-existing test relied on the per-worktree call shape (e.g. checked which prefix `listProcs` was invoked with), update it to read the post-bucket result from the state instead.

- [ ] **Step 7.12: Run full test suite + vet**

Run: `go vet ./... && go test ./... -race`
Expected: PASS across all packages.

- [ ] **Step 7.13: Commit**

```bash
git add aggregator/types.go aggregator/aggregator.go aggregator/loop.go aggregator/aggregator_test.go
git commit -m "feat(aggregator): batch procs walk once per refresh"
```

---

## Task 8: Doc flips

Update the project docs to reflect macOS-first.

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/handoff.md`

- [ ] **Step 8.1: Update the `procs/` architecture bullet in CLAUDE.md**

In `CLAUDE.md`, find the architecture bullet for procs (around line 58):

```markdown
- `procs/` — process listing by cwd. Linux-first (`/proc/*/cwd`); macOS stubbed with a build tag.
```

Replace with:

```markdown
- `procs/` — process listing by cwd. macOS-first (raw `proc_info` syscall, no cgo); Linux supported (`/proc/*/cwd`). Other platforms return `ErrUnsupported` and the aggregator soft-degrades.
```

- [ ] **Step 8.2: Update the "Linux-first" hard rule**

In `CLAUDE.md`, find the hard rule (around line 73):

```markdown
- Linux-first. macOS process detection is a stubbed build-tag file; degrade gracefully, don't crash.
```

Replace with:

```markdown
- macOS-first; Linux supported. Other platforms degrade gracefully — never crash on missing OS support.
```

- [ ] **Step 8.3: Update the handoff portability bullet**

In `docs/handoff.md`, find the line (around line 121):

```markdown
- **Process detection portability.** Walking `/proc/*/cwd` on Linux vs. `lsof` on macOS. I'm primarily on Linux/WSL2 — fine to make Linux first-class and stub macOS, but worth being explicit.
```

Replace with:

```markdown
- **Process detection portability.** macOS uses the `proc_info` syscall (`proc_listpids` + `proc_pidinfo(PROC_PIDVNODEPATHINFO)` + `sysctl(KERN_PROCARGS2)`) via raw `syscall.Syscall6`, no cgo. Linux walks `/proc/*/cwd`. Both go through a single `enumerate(ctx)` seam so `procs/procs.go` owns the prefix filter.
```

- [ ] **Step 8.4: Commit**

```bash
git add CLAUDE.md docs/handoff.md
git commit -m "docs: flip procs platform note to macOS-first"
```

---

## Task 9: Final validation + simplify pass + open PR

Run the merge gate, do the project-mandated simplify pass on the cumulative diff, then open the PR.

- [ ] **Step 9.1: Run the full test suite**

Run: `go vet ./... && go build ./... && go test ./... -race`
Expected: PASS.

- [ ] **Step 9.2: Run the TUI demo (optional but recommended on darwin)**

If darwin host: build and run the demo, eyeball that the procs column populates.

Run: `go build -o canopy . && ./canopy demo --script=tui/testdata/scripts/operational-base.txt`
(Use whichever script exists; `ls tui/testdata/scripts/` to pick one.)

Expected: TUI renders; procs column has entries for any worktree with active processes inside it.

- [ ] **Step 9.3: Run the simplify skill on the full branch diff**

Per `CLAUDE.md`: "After committing and before opening a PR, run the `/simplify` skill over the diff."

Invoke `superpowers:simplify` (or the in-repo `simplify` skill — whichever is registered) on the cumulative branch diff:

```bash
git diff main...HEAD
```

Address whatever it surfaces — likely small things (unused imports, redundant `if`s, etc.). Commit fixes as a separate `chore: simplify ...` commit.

- [ ] **Step 9.4: Push the branch**

Run: `git push -u origin feat/procs-macos`

- [ ] **Step 9.5: Open the PR**

Run:

```bash
gh pr create --title "feat(procs): macOS implementation + batch enumeration API" --body "$(cat <<'EOF'
## Summary

- macOS-first procs implementation via raw `proc_info` syscall + `KERN_PROCARGS2` (no cgo, no new direct deps beyond promoting `x/sys/unix`).
- New `ListByCwdPrefixes` batch API. The aggregator now walks the process table **once per refresh** instead of once per worktree.
- Filtering centralised in `procs/procs.go`; each platform file exposes only `enumerate(ctx)`.
- Docs flipped to macOS-first.

Spec: `docs/superpowers/specs/2026-05-18-procs-macos-design.md`.

## Test plan

- [ ] `go test ./... -race` green on darwin (local).
- [ ] `go test ./... -race` green on linux (CI).
- [ ] `procs/procs_darwin_test.go` smoke: real `systemEnumerate` finds the test pid with non-empty Cwd/Args/Command.
- [ ] `aggregator/aggregator_test.go::TestRefreshAll_CallsProcsOnce`: regression for the N-walks-per-refresh waste.
- [ ] Manual darwin run: `./canopy demo --script=...` shows non-empty procs columns for worktrees with active processes.
EOF
)"
```

- [ ] **Step 9.6: Mark all tasks complete**

```bash
echo "Done."
```

---

## Self-review notes

**Spec coverage:**
- Public API (`ListByCwdPrefix` + `ListByCwdPrefixes`) — Task 1 + 2.
- Pure-Go darwin syscalls (`proc_info` + `KERN_PROCARGS2`) — Tasks 3, 4, 5.
- Test seam (`var enumerator`) — Task 2.1; smoke test Task 6.
- Aggregator one-walk-per-refresh — Task 7.
- Docs flips — Task 8.
- `gopsutil` is referenced in the spec as inspiration only; no dep added.

**Risk re-check:**
- `procVNodePathInfoSize = 2352` is pinned to the xnu header; if a future macOS SDK changes the struct, the smoke test in Task 6 will catch it (Cwd mismatch).
- `unix.SysctlRaw("kern.procargs2", pid)` — verified this signature exists in `golang.org/x/sys@v0.38.0`. If not on the local module version, fall back to building the mib manually.
