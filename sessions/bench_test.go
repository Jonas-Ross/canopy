package sessions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// BenchmarkScanFileMeta_Synthetic exercises scanFileMeta against an
// in-memory JSONL blob that mimics the shape of real Claude Code files:
// a small head of meta lines, a handful of conversation events with one
// fat assistant message, and a long tail of meta + side-band noise.
// Hermetic — no dependency on ~/.claude/projects.
func BenchmarkScanFileMeta_Synthetic(b *testing.B) {
	blob := buildSyntheticJSONL()
	b.SetBytes(int64(len(blob)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := scanFileMeta(bytes.NewReader(blob)); err != nil {
			b.Fatalf("scanFileMeta: %v", err)
		}
	}
}

func buildSyntheticJSONL() []byte {
	var sb strings.Builder

	// Head: a small batch of meta lines (no uuid).
	metaLines := []string{
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"permission-mode","mode":"plan"}`,
		`{"type":"ai-title","title":"sample session"}`,
		`{"type":"agent-name","name":"foo"}`,
	}
	for _, l := range metaLines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}

	// First conv event — sets first cwd + startedAt.
	sb.WriteString(`{"type":"user","uuid":"u-first","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/repo","message":{"role":"user","content":"start"}}`)
	sb.WriteByte('\n')

	// A few <synthetic> assistant lines that must be skipped over.
	for i := 0; i < 2; i++ {
		sb.WriteString(`{"type":"assistant","uuid":"a-syn","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/repo","message":{"model":"<synthetic>","content":[]}}`)
		sb.WriteByte('\n')
	}

	// A "fat" assistant turn carrying a 1 KB tool-input blob. Repeated 40x
	// to simulate a real session's middle. The model is set on the first
	// of these and reused — the parser must short-circuit message
	// decoding after the first non-synthetic match.
	fat := `{"type":"assistant","uuid":"a-fat","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/repo","message":{"model":"claude-opus-4-7","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + strings.Repeat("x", 1024) + `"}}]}}`
	for i := 0; i < 40; i++ {
		sb.WriteString(fat)
		sb.WriteByte('\n')
	}

	// A pile of side-band lines (uuid present, type=system|attachment).
	// These pass UUID gating but must be rejected by type-gating.
	for i := 0; i < 20; i++ {
		sb.WriteString(`{"type":"system","uuid":"s","timestamp":"2026-01-01T00:01:00.000Z","cwd":"/repo"}`)
		sb.WriteByte('\n')
		sb.WriteString(`{"type":"attachment","uuid":"at","timestamp":"2026-01-01T00:01:00.000Z","cwd":"/repo"}`)
		sb.WriteByte('\n')
	}

	// Tail: meta noise + a last conv event so cwd transition is exercised.
	for i := 0; i < 30; i++ {
		sb.WriteString(`{"type":"file-history-snapshot","files":[]}`)
		sb.WriteByte('\n')
	}
	sb.WriteString(`{"type":"user","uuid":"u-last","timestamp":"2026-01-01T00:02:00.000Z","cwd":"/repo/sub","message":{"role":"user","content":"end"}}`)
	sb.WriteByte('\n')

	return []byte(sb.String())
}
