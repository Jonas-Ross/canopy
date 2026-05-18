package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonasross/canopy/internal/demo"
)

// TestDemoScript_OpenPRWithPR exercises the demo subcommand end-to-end:
// fixture build, aggregator wiring, scripted keypresses, captured frame.
//
// Regression guard: when cmd/root.go forgot PRCache the same `keys p` flow
// would surface "no PR for feat/auth" instead of "opening …". Asserting on
// the "opening" notice in the captured frame catches that class of wiring
// bug for cmd/demo.go independently of cmd/root.go.
func TestDemoScript_OpenPRWithPR(t *testing.T) {
	demo.RequireGit(t)

	tmp := t.TempDir()
	script := filepath.Join(tmp, "open_pr.txt")
	capture := filepath.Join(tmp, "frame.txt")
	body := "width 140\nheight 40\nwait 200ms\nkey down\nkey down\nkeys p\ncapture " + capture + "\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"demo", "--script", script})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("demo execute: %v", err)
	}

	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	frame := string(got)
	if !strings.Contains(frame, "opening ") || !strings.Contains(frame, "feat/auth") {
		t.Errorf("captured frame missing 'opening …' notice for feat/auth.\nframe:\n%s", frame)
	}
	if strings.Contains(frame, "no PR for") {
		t.Errorf("captured frame contains 'no PR' — PRCache wiring regression.\nframe:\n%s", frame)
	}
}

func TestDemoScript_ProcsCollapseExpand(t *testing.T) {
	demo.RequireGit(t)

	tmp := t.TempDir()
	collapsedPath := filepath.Join(tmp, "collapsed.txt")
	expandedPath := filepath.Join(tmp, "expanded.txt")
	recollapsedPath := filepath.Join(tmp, "recollapsed.txt")
	script := filepath.Join(tmp, "procs.txt")
	body := "" +
		"width 140\nheight 40\nwait 200ms\n" +
		"keys jj\nresolve\n" +
		"capture " + collapsedPath + "\n" +
		"keys P\nresolve\n" +
		"capture " + expandedPath + "\n" +
		"keys P\nresolve\n" +
		"capture " + recollapsedPath + "\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"demo", "--script", script})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("demo execute: %v", err)
	}

	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}
	collapsed := read(collapsedPath)
	expanded := read(expandedPath)
	recollapsed := read(recollapsedPath)

	// Collapsed: top-tier procs visible, "+N more (P)" hint, tail hidden.
	if !strings.Contains(collapsed, "Processes") {
		t.Errorf("collapsed frame missing Processes header:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "+9 more (P)") {
		t.Errorf("collapsed frame missing '+9 more (P)':\n%s", collapsed)
	}
	if strings.Contains(collapsed, "Cursor Helper") {
		t.Errorf("collapsed frame should not show tail proc 'Cursor Helper':\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "11201") {
		t.Errorf("collapsed frame missing top claude pid 11201:\n%s", collapsed)
	}

	// Expanded: every proc visible, no "more" line.
	if !strings.Contains(expanded, "Cursor Helper") {
		t.Errorf("expanded frame missing tail proc 'Cursor Helper':\n%s", expanded)
	}
	if strings.Contains(expanded, "more (P)") {
		t.Errorf("expanded frame should not contain '+N more (P)':\n%s", expanded)
	}

	// Recollapsed: matches collapsed view (idempotent toggle).
	if !strings.Contains(recollapsed, "+9 more (P)") {
		t.Errorf("recollapsed frame missing '+9 more (P)':\n%s", recollapsed)
	}
	if strings.Contains(recollapsed, "Cursor Helper") {
		t.Errorf("recollapsed frame should not show tail proc:\n%s", recollapsed)
	}
}

