package aggregator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/sessions"
)

// BenchmarkSnapshot_RealRepo measures Snapshot end-to-end against the
// live ~/.claude/projects index and the current cwd's git repo. Skipped
// when either is absent so CI and fresh checkouts don't fail.
//
// Pairs with sessions/bench_test.go's BenchmarkOpen_RealClaudeProjects —
// together they cover the two timing surfaces M6 verification records.
func BenchmarkSnapshot_RealRepo(b *testing.B) {
	home, err := os.UserHomeDir()
	if err != nil {
		b.Skipf("UserHomeDir: %v", err)
	}
	projects := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(projects); err != nil {
		b.Skipf("%s not present: %v", projects, err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		b.Skipf("Getwd: %v", err)
	}

	store, err := sessions.Open(projects)
	if err != nil {
		b.Fatalf("sessions.Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	agg, err := New(Config{
		Repos:        []Repo{{Root: cwd, Name: filepath.Base(cwd)}},
		SessionStore: store,
		PRCache:      pr.NewCache(30 * time.Second),
	})
	if err != nil {
		b.Fatalf("aggregator.New: %v", err)
	}

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := agg.Snapshot(ctx); err != nil {
			b.Fatalf("Snapshot: %v", err)
		}
	}
}
