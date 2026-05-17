//go:build !linux

package procs

import (
	"context"
	"errors"
	"testing"
)

// TestListByCwdPrefix_ReturnsErrUnsupported confirms the platform stub
// surfaces ErrUnsupported (matchable via errors.Is) so callers can
// branch on it and degrade gracefully. The prefix is arbitrary; the
// stub does not look at it.
func TestListByCwdPrefix_ReturnsErrUnsupported(t *testing.T) {
	got, err := ListByCwdPrefix(context.Background(), "/any/path")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want errors.Is(err, ErrUnsupported)", err)
	}
	if got != nil {
		t.Fatalf("want nil result on unsupported platform, got %+v", got)
	}
}
