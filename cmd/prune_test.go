package cmd

import (
	"strings"
	"testing"
)

func TestPrune_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "prune")
	if !strings.Contains(got, "canopy prune: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}
