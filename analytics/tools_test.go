package analytics

import (
	"reflect"
	"testing"
	"time"
)

func TestToolDistribution_byModelThenTool(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, []sessionSpec{
		{id: "s1", model: "claude-opus-4-7", started: day(5, 22, 10), updated: day(5, 22, 11),
			tools: map[string]int{"Bash": 10, "Read": 5, "Edit": 3}},
		{id: "s2", model: "claude-opus-4-7", started: day(5, 22, 14), updated: day(5, 22, 15),
			tools: map[string]int{"Bash": 2, "Grep": 1}},
		{id: "s3", model: "claude-sonnet-4-6", started: day(5, 21, 9), updated: day(5, 21, 10),
			tools: map[string]int{"Read": 4, "Bash": 1}},
	})

	got, err := ToolDistribution(store, now.AddDate(0, 0, -30), now)
	if err != nil {
		t.Fatal(err)
	}

	// Expect: opus → Bash 12, Read 5, Edit 3, Grep 1; sonnet → Read 4, Bash 1.
	// Sorted: model asc, then count desc within model.
	want := []ToolUsage{
		{"claude-opus-4-7", "Bash", 12},
		{"claude-opus-4-7", "Read", 5},
		{"claude-opus-4-7", "Edit", 3},
		{"claude-opus-4-7", "Grep", 1},
		{"claude-sonnet-4-6", "Read", 4},
		{"claude-sonnet-4-6", "Bash", 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ToolDistribution mismatch:\n got %+v\nwant %+v", got, want)
	}
}
