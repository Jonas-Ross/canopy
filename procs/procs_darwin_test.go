//go:build darwin

package procs

import (
	"context"
	"errors"
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
		if len(p.Args) > 0 && !strings.Contains(p.Args[0], p.Command) {
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestCommandFromArgs covers the pure helper's edge cases.
// commandFromArgs has no syscall dependency so it can be unit-tested
// independently of systemEnumerate.
func TestCommandFromArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty", nil, ""},
		{"empty slice", []string{}, ""},
		{"no slash", []string{"bash"}, "bash"},
		{"absolute path", []string{"/bin/zsh"}, "zsh"},
		{"deep path", []string{"/usr/local/bin/claude", "--print"}, "claude"},
		{"empty first arg", []string{""}, ""},
		{"trailing slash", []string{"/foo/"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandFromArgs(tc.args); got != tc.want {
				t.Errorf("commandFromArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
