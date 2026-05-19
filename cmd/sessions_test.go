package cmd

import (
	"strings"
	"testing"
)

func TestSessionsList_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "sessions", "list")
	if !strings.Contains(got, "canopy sessions list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessionsTail_PrintsStub(t *testing.T) {
	got := runRootCmd(t, "sessions", "tail")
	if !strings.Contains(got, "canopy sessions tail: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessions_HelpListsChildren(t *testing.T) {
	got := runRootCmd(t, "sessions", "--help")
	for _, child := range []string{"list", "tail"} {
		if !strings.Contains(got, child) {
			t.Errorf("sessions --help missing child %q; got %q", child, got)
		}
	}
}
