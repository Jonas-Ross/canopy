package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLogCleanupErr_WritesToStderrOnError(t *testing.T) {
	var buf bytes.Buffer
	logCleanupErr(&buf, errors.New("remove /tmp/canopy-demo-xyz: permission denied"))

	got := buf.String()
	if got == "" {
		t.Fatal("logCleanupErr wrote nothing for non-nil err")
	}
	if !strings.Contains(got, "cleanup") {
		t.Errorf("stderr missing 'cleanup' label:\n%s", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("stderr missing underlying error text:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("stderr line not newline-terminated: %q", got)
	}
}

func TestLogCleanupErr_NilErrorIsSilent(t *testing.T) {
	var buf bytes.Buffer
	logCleanupErr(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("logCleanupErr wrote %q for nil err, want silent", buf.String())
	}
}
