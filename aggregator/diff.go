package aggregator

import (
	"slices"

	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/procs"
	"github.com/jonasross/canopy/sessions"
)

func worktreeStatesEqual(a, b WorktreeState) bool {
	if a.Repo != b.Repo {
		return false
	}
	if a.Worktree != b.Worktree {
		return false
	}
	if !prsEqual(a.PR, b.PR) {
		return false
	}
	if a.PRStale != b.PRStale {
		return false
	}
	if !procsEqual(a.Procs, b.Procs) {
		return false
	}
	if !sessionPtrsEqual(a.Live, b.Live) {
		return false
	}
	return sessionSlicesEqual(a.Recent, b.Recent)
}

func prsEqual(a, b *pr.PR) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// procsEqual compares slices by pid only; order is allowed to vary
// between scans and cmdline/cwd are decoration for diff purposes.
func procsEqual(a, b []procs.Process) bool {
	if len(a) != len(b) {
		return false
	}
	pidsA := make([]int, 0, len(a))
	pidsB := make([]int, 0, len(b))
	for _, p := range a {
		pidsA = append(pidsA, p.Pid)
	}
	for _, p := range b {
		pidsB = append(pidsB, p.Pid)
	}
	slices.Sort(pidsA)
	slices.Sort(pidsB)
	return slices.Equal(pidsA, pidsB)
}

func sessionPtrsEqual(a, b *sessions.Session) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID
}

func sessionSlicesEqual(a, b []*sessions.Session) bool {
	return slices.EqualFunc(a, b, func(x, y *sessions.Session) bool {
		if x == nil || y == nil {
			return x == y
		}
		return x.ID == y.ID
	})
}
