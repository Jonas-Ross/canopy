//go:build linux

package procs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// fakeProc is a small builder for a /proc-shaped tree under a test
// directory. It owns nothing the test doesn't already know; the only
// reason it exists is to centralize the file-layout decisions
// (cwd symlink, comm with trailing newline, cmdline NUL-separated)
// that the production reader depends on.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	root := t.TempDir()
	return &fakeProc{t: t, root: root}
}

// addPid materializes /<root>/<pid>/cwd → cwdTarget, /comm, /cmdline.
// cwdTarget is the literal symlink target; the caller decides whether
// it points at a real directory under root or anywhere else.
func (f *fakeProc) addPid(pid int, cwdTarget, comm string, cmdline []byte) {
	f.t.Helper()
	pidDir := filepath.Join(f.root, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", pidDir, err)
	}
	if err := os.Symlink(cwdTarget, filepath.Join(pidDir, "cwd")); err != nil {
		f.t.Fatalf("symlink cwd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "comm"), []byte(comm), 0o644); err != nil {
		f.t.Fatalf("write comm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), cmdline, 0o644); err != nil {
		f.t.Fatalf("write cmdline: %v", err)
	}
}

// addPidWithNonSymlinkCwd creates a pid dir whose "cwd" is a regular
// directory rather than a symlink. os.Readlink on it returns EINVAL,
// which the production reader must swallow.
func (f *fakeProc) addPidWithNonSymlinkCwd(pid int) {
	f.t.Helper()
	pidDir := filepath.Join(f.root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(pidDir, "cwd"), 0o755); err != nil {
		f.t.Fatalf("mkdir cwd-as-dir: %v", err)
	}
}

// addNonNumericEntry drops a directory or file whose name is not a pid
// (self, meminfo, …). The reader must ignore it.
func (f *fakeProc) addNonNumericEntry(name string, asDir bool) {
	f.t.Helper()
	p := filepath.Join(f.root, name)
	if asDir {
		if err := os.MkdirAll(p, 0o755); err != nil {
			f.t.Fatalf("mkdir non-numeric: %v", err)
		}
		return
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		f.t.Fatalf("write non-numeric: %v", err)
	}
}

// useFakeProc points procFSRoot at the fake tree and restores it via
// t.Cleanup. Returning the root keeps the cwd-target plumbing local to
// each test.
func useFakeProc(t *testing.T, f *fakeProc) {
	t.Helper()
	orig := procFSRoot
	procFSRoot = f.root
	t.Cleanup(func() { procFSRoot = orig })
}

func TestListByCwdPrefix_FindsMatching(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	f.addPid(101, "/work/canopy", "claude", []byte("claude\x00"))
	f.addPid(202, "/work/canopy/sub", "go", []byte("go\x00test\x00"))
	f.addPid(303, "/elsewhere", "bash", []byte("bash\x00"))

	got, err := ListByCwdPrefix(context.Background(), "/work/canopy")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 processes, got %d: %+v", len(got), got)
	}
	if got[0].Pid != 101 || got[1].Pid != 202 {
		t.Fatalf("want pids [101,202] sorted asc, got [%d,%d]", got[0].Pid, got[1].Pid)
	}
}

func TestListByCwdPrefix_PopulatesCommandAndArgs(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	// comm has the trailing newline the kernel writes; cmdline is
	// NUL-separated with a trailing NUL (canonical kernel layout).
	f.addPid(42, "/work/canopy", "claude\n", []byte("claude\x00--print\x00hello\x00"))

	got, err := ListByCwdPrefix(context.Background(), "/work/canopy")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 process, got %d", len(got))
	}
	if got[0].Command != "claude" {
		t.Errorf("Command=%q, want %q", got[0].Command, "claude")
	}
	wantArgs := []string{"claude", "--print", "hello"}
	if !reflect.DeepEqual(got[0].Args, wantArgs) {
		t.Errorf("Args=%v, want %v", got[0].Args, wantArgs)
	}
}

func TestListByCwdPrefix_SkipsPidsWithReadlinkErrors(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	// Healthy match.
	f.addPid(101, "/work/canopy", "claude", []byte("claude\x00"))
	// Symlink target doesn't exist on disk — but readlink itself
	// succeeds (readlink reads the link, not the target). To force
	// an actual readlink failure, install a directory called "cwd"
	// instead of a symlink: readlink returns EINVAL.
	f.addPidWithNonSymlinkCwd(202)

	got, err := ListByCwdPrefix(context.Background(), "/work/canopy")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if len(got) != 1 || got[0].Pid != 101 {
		t.Fatalf("want only pid 101, got %+v", got)
	}
}

func TestListByCwdPrefix_EmptyPrefixMatchesAll(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	f.addPid(1, "/a", "a", []byte("a\x00"))
	f.addPid(2, "/b", "b", []byte("b\x00"))
	f.addPid(3, "/c/d", "d", []byte("d\x00"))

	got, err := ListByCwdPrefix(context.Background(), "")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want all 3 processes, got %d: %+v", len(got), got)
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].Pid != want {
			t.Errorf("pid[%d]=%d, want %d", i, got[i].Pid, want)
		}
	}
}

func TestListByCwdPrefix_NoMatches_ReturnsEmptyNotNil(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	f.addPid(1, "/elsewhere", "x", []byte("x\x00"))

	got, err := ListByCwdPrefix(context.Background(), "/work/canopy")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if got == nil {
		t.Fatalf("want empty non-nil slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("want empty result, got %+v", got)
	}
}

func TestListByCwdPrefix_IgnoresNonNumericDirs(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	f.addNonNumericEntry("self", true)
	f.addNonNumericEntry("thread-self", true)
	f.addNonNumericEntry("meminfo", false)
	f.addPid(777, "/work/canopy", "claude", []byte("claude\x00"))

	got, err := ListByCwdPrefix(context.Background(), "/work/canopy")
	if err != nil {
		t.Fatalf("ListByCwdPrefix: %v", err)
	}
	if len(got) != 1 || got[0].Pid != 777 {
		t.Fatalf("want only pid 777, got %+v", got)
	}
}

func TestListByCwdPrefix_ContextCancelled(t *testing.T) {
	f := newFakeProc(t)
	useFakeProc(t, f)

	f.addPid(1, "/a", "a", []byte("a\x00"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ListByCwdPrefix(ctx, "/a")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
