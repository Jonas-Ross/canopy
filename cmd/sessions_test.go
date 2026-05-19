package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionsList_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy sessions list: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessionsTail_PrintsStub(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "tail"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "canopy sessions tail: not yet implemented (M5 placeholder)") {
		t.Errorf("stub message missing; got %q", got)
	}
}

func TestSessions_HelpListsChildren(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"sessions", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, child := range []string{"list", "tail"} {
		if !strings.Contains(got, child) {
			t.Errorf("sessions --help missing child %q; got %q", child, got)
		}
	}
}
