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

