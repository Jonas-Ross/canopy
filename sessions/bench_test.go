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
// in-memory JSONL blob shaped like a real Claude Code file. Hermetic —
// no dependency on ~/.claude/projects.
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
	writeLine := func(s string) {
		sb.WriteString(s)
		sb.WriteByte('\n')
	}

	for _, l := range []string{
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"permission-mode","mode":"plan"}`,
		`{"type":"ai-title","title":"sample session"}`,
		`{"type":"agent-name","name":"foo"}`,
	} {
		writeLine(l)
	}

	writeLine(`{"type":"user","uuid":"u-first","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/repo","message":{"role":"user","content":"start"}}`)

	for i := 0; i < 2; i++ {
		writeLine(`{"type":"assistant","uuid":"a-syn","timestamp":"2026-01-01T00:00:01.000Z","cwd":"/repo","message":{"model":"<synthetic>","content":[]}}`)
	}

	// 40× fat assistant turn with a 1 KB tool-input blob — model capture
	// must short-circuit after the first hit, not re-decode every line.
	fat := `{"type":"assistant","uuid":"a-fat","timestamp":"2026-01-01T00:00:02.000Z","cwd":"/repo","message":{"model":"claude-opus-4-7","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + strings.Repeat("x", 1024) + `"}}]}}`
	for i := 0; i < 40; i++ {
		writeLine(fat)
	}

	for i := 0; i < 20; i++ {
		writeLine(`{"type":"system","uuid":"s","timestamp":"2026-01-01T00:01:00.000Z","cwd":"/repo"}`)
		writeLine(`{"type":"attachment","uuid":"at","timestamp":"2026-01-01T00:01:00.000Z","cwd":"/repo"}`)
	}

	for i := 0; i < 30; i++ {
		writeLine(`{"type":"file-history-snapshot","files":[]}`)
	}
	writeLine(`{"type":"user","uuid":"u-last","timestamp":"2026-01-01T00:02:00.000Z","cwd":"/repo/sub","message":{"role":"user","content":"end"}}`)

	return []byte(sb.String())
}
