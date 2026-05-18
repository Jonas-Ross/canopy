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
