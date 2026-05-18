# Procs on macOS: pure-Go implementation + batch enumeration API

**Status:** approved, awaiting implementation plan
**Date:** 2026-05-18
**Owner:** Jonas
**Branch:** `feat/procs-macos`

## Background

`procs/` enumerates local processes filtered by working-directory prefix. The
aggregator uses it to attach "processes running in this worktree" (especially
`claude`) to per-worktree state.

Today `procs_darwin.go` returns `ErrUnsupported`. The aggregator soft-degrades:
the worktree's `Procs` field stays empty and the TUI hides the column.

The project is now macOS-first (Jonas's daily platform). The procs package needs
a real darwin implementation. While we're touching the API, the aggregator's
N-walks-per-refresh pattern also needs fixing — it doesn't scale on macOS even
with the fastest possible per-pid implementation.

## Goals

1. Replace the darwin stub with a working enumerator that has Linux-parity
   semantics: `Pid`, `Cwd`, `Command`, `Args` populated for every visible
   process whose cwd is under a given prefix.
2. Keep the package free of cgo and third-party dependencies.
3. Eliminate the per-worktree enumeration cost: a single refresh cycle should
   walk the process table at most once, not once per worktree.
4. Flip project docs to "macOS-first; Linux supported."

## Non-goals

- Reading process state Canopy doesn't already use (memory, fds, signals, etc).
- A daemon, watcher, or kqueue subscription. v1 stays poll-based.
- Windows/BSD support. They stay `ErrUnsupported`.
- Process kill, exec, signal — `procs/` is read-only.
- Privileged enumeration. We see what the user's uid can see; root-owned
  processes from other users surface as readlink/permission failures and are
  silently skipped, same as today.

## Public API

Two entry points. Both live in cross-platform `procs/procs.go`.

```go
// ListByCwdPrefix returns processes whose cwd has the given prefix.
// Existing single-prefix entry point, semantics unchanged.
func ListByCwdPrefix(ctx context.Context, prefix string) ([]Process, error)

// ListByCwdPrefixes returns processes grouped by which prefix they match,
// using a single enumeration of the process table.
//
// A process whose cwd matches multiple prefixes appears under EACH matching
// prefix. The caller is responsible for any longest-prefix deduplication
// (the aggregator already does this against its siblings list).
//
// Bare paths are matched as exact-prefix; pass a trailing slash to require
// a directory boundary. An empty prefix in the slice matches every visible
// process. Prefixes with zero matches map to an empty (non-nil) slice.
// The result map always has one entry per input prefix.
//
// Ordering within each bucket is Pid ascending, matching ListByCwdPrefix.
func ListByCwdPrefixes(ctx context.Context, prefixes []string) (map[string][]Process, error)
```

`Process` and `ErrUnsupported` are unchanged.

### Why batch returns "all matching prefixes" not "deepest match"

The aggregator already owns longest-prefix attribution via `longestMatchingPath`
against its siblings list (`aggregator.buildState`). Pushing that logic into
`procs/` would require the package to understand sibling hierarchies, which it
has no business knowing. Returning every match per prefix keeps `procs/` a pure
data-access library and matches the package's CLAUDE.md guidance ("No domain
logic in `sessions/`" — same spirit applies here).

Memory cost is bounded: a process matching K prefixes appears K times. With
~1500 procs on a typical Mac and ~10 worktrees in the bucket-matching tail, the
duplicated slice headers are negligible.

## Architecture

### Shared filtering, platform-specific enumeration

Today the linux file owns both enumeration AND filtering. Refactor so:

- `procs/procs.go` (cross-platform) owns `ListByCwdPrefix`, `ListByCwdPrefixes`,
  and the prefix-bucketing logic. Both public APIs route through
  `ListByCwdPrefixes` internally; single-prefix is a one-element slice.
- `procs/procs_linux.go` exposes one function: `enumerate(ctx) ([]Process, error)`.
  It walks `procFSRoot` and returns the full list (no filtering). Existing
  test seam (`procFSRoot` swap) stays.
- `procs/procs_darwin.go` exposes the same `enumerate(ctx)` function via
  raw syscalls. A package-level `enumerate` var (or equivalent seam) lets
  tests inject a fake list.
- `procs/procs_other.go` exposes `enumerate(ctx) (nil, ErrUnsupported)`.

The cross-platform layer calls `enumerate` once per `ListByCwdPrefixes` call,
then buckets by prefix. Combined with the aggregator change below, this yields
the target invariant: one full walk per refresh cycle on every platform,
regardless of worktree count.

### Darwin enumeration: raw syscalls, no cgo

Two pieces of information per pid:

**Pid list:** `proc_listpids(PROC_ALL_PIDS, 0, buf, bufSize)` — syscall #336
(`SYS_PROC_INFO`) with `callnum = PROC_INFO_CALL_LISTPIDS`. Returns an array of
int32 pids. We size the buffer to twice the result of an initial size-probe
call (`proc_listpids(... NULL, 0)` returns required bytes) to absorb forks
between the probe and the read.

**Per-pid cwd:** `proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, buf, sizeof(buf))`
— same syscall #336 with `callnum = PROC_INFO_CALL_PIDINFO`. Returns a
`proc_vnodepathinfo` struct whose `pvi_cdir.vip_path` is the cwd as a
null-terminated C string. The struct also carries `pvi_rdir` (root dir, not
needed).

**Per-pid command line:** `sysctl({CTL_KERN, KERN_PROCARGS2, pid})` returns a
packed buffer:
```
int32 argc
char  exec_path[]   // null-terminated, possibly followed by zero padding
char  argv[argc][]  // each null-terminated
char  envp[]        // env vars after argv, ignored
```
Parse argc, skip the exec path and its zero padding, then split the next argc
null-terminated strings into `Args`. `Command` is `Args[0]` basename if Args
is non-empty, else `""`.

Note: an alternate source for `Command` on darwin is
`proc_pidinfo(pid, PROC_PIDTBSDINFO)` which returns `proc_bsdinfo.pbi_comm`
— the kernel-tracked short name, structurally identical to Linux's `comm`.
We deliberately don't call it to save one syscall per pid (~30–50µs ×
~1500 procs = ~50–75ms saved per walk). The argv[0]-basename divergence from
Linux is acceptable: this field is best-effort metadata and `Args` carries
the canonical truth either way.

Dispatched via `golang.org/x/sys/unix` where available (`unix.SysctlRaw` for
KERN_PROCARGS2, `unix.Syscall6` for proc_info). The package is already in
`go.sum` transitively via existing deps; if not, it's the standard pure-Go
syscall library and acceptable per CLAUDE.md's "discuss before adding any
library" — flagging it here as the addition.

### Error handling on darwin

Mirror the Linux path: per-pid errors are swallowed silently. A process can
exit between the pid list and the `proc_pidinfo` call (ESRCH), or be owned by
another user (EPERM). One unreadable pid must not abort the walk.

The full-walk syscall (`proc_listpids` size probe) failing IS surfaced — that's
a real "cannot enumerate" condition, equivalent to `os.ReadDir("/proc")`
failing on Linux.

### Context cancellation on darwin

Check `ctx.Err()` between pids in the per-pid loop. Same granularity as Linux.
The pid-list syscall itself is uninterruptible (it's a single fast syscall), so
cancellation can't happen mid-list — but the per-pid pass dominates wall time
and is interruptible.

## Aggregator integration

### Today

```go
// aggregator.buildState, called per worktree
if ps, err := a.cfg.listProcs(ctx, state.Worktree.Path); err == nil && ps != nil {
    state.Procs = ps[:0]
    for _, p := range ps {
        if longestMatchingPath(p.Cwd, siblings) == state.Worktree.Path {
            state.Procs = append(state.Procs, p)
        }
    }
}
```

Called once per worktree per refresh. N worktrees → N full enumerations.

### Target

The refresh entry points (`refreshAll`, `refreshOne`) gain a procs-snapshot
step that runs once per refresh, before `buildState` calls. The snapshot
contains `map[string][]Process` keyed by every worktree path across every repo.
`buildState` consumes its worktree's bucket directly and applies the existing
longest-prefix-against-siblings filter.

`refreshAll` builds the full prefix list from all repos' worktrees and calls
`ListByCwdPrefixes` once. `refreshOne` for a single worktree path passes a
one-element slice — it still pays the full-enumeration cost, but only one
walk, and the call site stays simple.

The `listProcs` test seam in `Config` becomes `listProcsByPrefixes` with the
batch signature. Tests in `aggregator_test.go` get a one-line shim adjustment
(the `fakeSources.listProcs` method returns the map shape instead of a slice).

### Per-worktree refresh: the open call

`refreshOne` could either:
- (a) walk the full process table for one worktree — the obvious thing, and
  what we do today; or
- (b) re-use a recent cached snapshot from the last `refreshAll` if within some
  TTL.

Picking (a). Time-based caches are fragile, fsnotify-triggered refreshes are
rare enough that paying the full walk is fine, and a Mac-fast enumerate is
~30ms anyway. We can revisit if telemetry shows it matters.

## Test strategy

### procs package

**Cross-platform tests** in `procs/procs_test.go` (no build tag) cover the
bucketing logic in `ListByCwdPrefixes` against an injected enumerator stub:
- multiple prefixes, deepest match isn't deduplicated (each matching prefix
  gets the proc)
- empty prefix matches everything
- prefix with no matches returns empty-non-nil bucket
- result map has exactly one entry per input prefix
- trailing-slash semantics (`/repo/` vs `/repo`)
- context cancellation propagates

**Linux tests** (`procs_linux_test.go`) keep the existing `procFSRoot` swap,
covering `enumerate` end-to-end against a fake `/proc` tree. The existing test
cases survive verbatim; only the production code being tested moves.

**Darwin tests** (`procs_darwin_test.go`, `//go:build darwin`) use the
`var enumerate = systemEnumerate` swap to inject fake process lists. Coverage:
- `enumerate` integration: a single live test that calls real `systemEnumerate`,
  finds the test binary's own pid in the result, and asserts cwd and argv are
  populated correctly. Lightweight smoke test for the syscalls.
- The bucketing/filtering tests live in the cross-platform file already.

The fake-list approach intentionally does NOT exercise the syscall wrappers in
unit tests. The smoke test covers them; correctness of the syscall layer is
established by the smoke test plus mirroring `gopsutil`'s well-trodden pattern.

### aggregator

Existing tests use a `fakeSources.listProcs(prefix) []Process` helper. They
update to return a per-prefix map. Test scenarios stay the same.

A new test verifies the single-call invariant: a refresh of N worktrees results
in exactly one call to `listProcsByPrefixes`. Counter on the fake.

## Risks and open questions

- **Raw syscall stability.** Syscall #336 (`SYS_PROC_INFO`) is macOS-private.
  Apple has kept it stable since 10.5 (2007). `lsof`, `ps`, every monitoring
  tool depends on it. Acceptable risk.
- **`x/sys/unix` as a dep.** It's the canonical pure-Go syscall library and
  already transitively in module graphs. Flagging it as a deliberate addition
  here per CLAUDE.md.
- **`KERN_PROCARGS2` truncation.** Long command lines (>~ARG_MAX) get
  truncated. The `argc` field is still correct; we slice what we have. Args may
  be partial but Pid/Cwd/Command stay correct. Acceptable.
- **Process owned by another user.** `proc_pidinfo` returns EPERM for processes
  outside the caller's uid. We skip silently. Matches Linux behavior for
  unreadable `/proc/<pid>/cwd`.

## Docs to update in this PR

- `CLAUDE.md`: "Linux-first" → "macOS-first; Linux supported." Plus the
  `procs/` package description line.
- `procs/procs.go`: package doc comment, `ErrUnsupported` doc comment, struct
  field doc comments referencing Linux paths.
- `docs/handoff.md:121`: "process detection portability" bullet — update to
  reflect macOS as primary.

## Out of scope (future PRs)

- Eviction cache or kqueue subscription for sub-30s freshness.
- Surfacing process state (sleeping/running/zombie) in the TUI.
- Pre-computing prefix tries for very large prefix sets — `ListByCwdPrefixes`
  is O(N_procs × N_prefixes × avg_prefix_len) today, fine for our scale.

## PR scope summary

One PR, one branch (`feat/procs-macos`):

1. Refactor `procs/` so `enumerate` is the platform-specific seam and
   `procs.go` owns filtering.
2. Implement `enumerate` on darwin via raw `proc_info` + `KERN_PROCARGS2`.
3. Add `ListByCwdPrefixes` public API.
4. Switch aggregator to one batched call per refresh.
5. Tests at every layer.
6. Doc flips: CLAUDE.md, handoff, package doc.

Self-validation: `go test ./... -race` green on darwin and CI Linux. The TUI
demo (`./canopy demo --script=...`) should now show non-empty procs columns
when the sandbox runs a long-lived process inside a worktree.
