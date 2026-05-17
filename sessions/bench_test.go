package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkOpen_RealClaudeProjects measures Open on the live
// ~/.claude/projects tree. Skipped when the directory is absent
// so CI and fresh checkouts don't fail.
func BenchmarkOpen_RealClaudeProjects(b *testing.B) {
	home, err := os.UserHomeDir()
	if err != nil {
		b.Skipf("UserHomeDir: %v", err)
	}
	root := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(root); err != nil {
		b.Skipf("%s not present: %v", root, err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store, err := Open(root)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := store.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}
