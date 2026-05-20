package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// runRootCmd dispatches args through rootCmd and returns the combined
// stdout+stderr output. Tests merge the two sinks because stubRunE writes
// to stderr while cobra's help routine writes to stdout — asserting on one
// alone would miss the other surface.
func runRootCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

func readCapture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	return string(b)
}

// assertFrameHasChrome guards against blank/minimal captures silently
// passing the substring assertions further down a demo-script test.
func assertFrameHasChrome(t *testing.T, label, frame string) {
	t.Helper()
	if lines := strings.Count(frame, "\n"); lines <= 5 {
		t.Fatalf("%s frame too short (%d newlines):\n%s", label, lines, frame)
	}
	if !strings.Contains(frame, "Canopy") {
		t.Fatalf("%s frame missing 'Canopy' title bar:\n%s", label, frame)
	}
}
