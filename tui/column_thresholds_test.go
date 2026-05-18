package tui_test

import (
	"strings"
	"testing"

	"github.com/jonasross/canopy/procs"
)

// Column-visibility threshold tests. Goldens cover widths 80/100/120/140;
// these pin the off-by-one boundary explicitly so a `>` vs `>=` regression
// surfaces. PR column at >=100, procs at >=120, detail pane at >=140.

func TestColumnThresholds_PRHiddenAt99(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 99, 30)
	view := stripANSI(m.View())
	// PR-column markers from the fixtures: "#42", "#43", "#44".
	for _, marker := range []string{"#42", "#43", "#44"} {
		if strings.Contains(view, marker) {
			t.Errorf("PR column visible at width=99 (marker %q); should be hidden until width=%d", marker, 100)
		}
	}
}

func TestColumnThresholds_PRVisibleAt100(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 100, 30)
	view := stripANSI(m.View())
	if !strings.Contains(view, "#42") {
		t.Errorf("PR column hidden at width=100; should be visible. View:\n%s", view)
	}
}

func TestColumnThresholds_ProcsHiddenAt119(t *testing.T) {
	fixtures := scenarioFixtures()
	fixtures[1].Procs = []procs.Process{
		{Pid: 1234, Cwd: fixtures[1].Path, Command: "claude", Args: []string{"--print"}},
	}
	m := buildModel(t, fixtures, 119, 30)
	view := stripANSI(m.View())
	// renderProcs emits "1*" for one claude proc. This marker only appears
	// inside the procs column, so it's a clean proxy for column visibility.
	if strings.Contains(view, "1*") {
		t.Errorf("procs column visible at width=119 (saw '1*' marker); should be hidden until width=%d. View:\n%s", 120, view)
	}
}

func TestColumnThresholds_ProcsVisibleAt120(t *testing.T) {
	fixtures := scenarioFixtures()
	fixtures[1].Procs = []procs.Process{
		{Pid: 1234, Cwd: fixtures[1].Path, Command: "claude", Args: []string{"--print"}},
	}
	m := buildModel(t, fixtures, 120, 30)
	view := stripANSI(m.View())
	if !strings.Contains(view, "1*") {
		t.Errorf("procs column hidden at width=120; should be visible. View:\n%s", view)
	}
}

func TestColumnThresholds_DetailHiddenAt139(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 139, 30)
	// Focus the second worktree so detail would have content if visible.
	m, _ = m.Update(sendKey('j'))
	view := stripANSI(m.View())
	// Detail pane renders "Sessions" header when visible; at width=139 it
	// must not appear.
	if strings.Contains(view, "Sessions") {
		t.Errorf("detail pane visible at width=139; should be hidden until width=140. View:\n%s", view)
	}
}

func TestColumnThresholds_DetailVisibleAt140(t *testing.T) {
	m := buildModel(t, scenarioFixtures(), 140, 30)
	m, _ = m.Update(sendKey('j'))
	view := stripANSI(m.View())
	if !strings.Contains(view, "Sessions") {
		t.Errorf("detail pane hidden at width=140; should be visible. View:\n%s", view)
	}
}
