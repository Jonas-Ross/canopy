package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonasross/canopy/sessions"
)

// usage mirrors sessions.TokenStats for test spec convenience.
type usage struct {
	Input         int
	Output        int
	CacheRead     int
	CacheCreation int
}

// sessionSpec describes one synthetic session for newTestStore.
type sessionSpec struct {
	id      string
	model   string
	started time.Time
	updated time.Time
	usage   usage
	tools   map[string]int // tool name → call count
	prompts int            // extra user prompt lines (in addition to the first)
	cwd     string         // override cwd; defaults to "/repo"
}

// day returns a time in 2026 UTC at midnight plus the given hour offset.
// month and dom are 1-based.
func day(month, dom, hour int) time.Time {
	return time.Date(2026, time.Month(month), dom, hour, 0, 0, 0, time.UTC)
}

// bucketsByDate indexes a []DayBucket by "YYYY-MM-DD" for easy lookup in tests.
func bucketsByDate(buckets []DayBucket) map[string]DayBucket {
	out := make(map[string]DayBucket, len(buckets))
	for _, b := range buckets {
		out[b.Date.Format("2006-01-02")] = b
	}
	return out
}

// newTestStore writes one JSONL file per sessionSpec under a temp directory
// and returns an open *sessions.Store backed by that tree.
//
// File layout mirrors ~/.claude/projects/<projectDir>/<sessionID>.jsonl.
// The project dir is derived from the cwd (or "test-project" by default).
func newTestStore(t *testing.T, specs []sessionSpec) *sessions.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")

	for _, spec := range specs {
		cwd := spec.cwd
		if cwd == "" {
			cwd = "/repo"
		}

		// Derive a stable project dir from cwd to ensure sessions with
		// the same cwd land in the same project bucket.
		projectDir := cwdToProjectDir(cwd)

		dir := filepath.Join(root, projectDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}

		path := filepath.Join(dir, spec.id+".jsonl")
		if err := writeSessionJSONL(path, spec, cwd); err != nil {
			t.Fatalf("writeSessionJSONL %s: %v", spec.id, err)
		}

		// Set file mtime to spec.updated so sessions.Open picks up the
		// right UpdatedAt (buildSession uses os.Stat mtime).
		if err := os.Chtimes(path, spec.updated, spec.updated); err != nil {
			t.Fatalf("chtimes %s: %v", spec.id, err)
		}
	}

	store, err := sessions.Open(root)
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// cwdToProjectDir turns a cwd path into a flat project directory name.
// Replaces slashes with dashes and drops leading slashes — same spirit
// as how Claude Code names its project dirs.
func cwdToProjectDir(cwd string) string {
	if cwd == "" {
		return "test-project"
	}
	// Simple: strip leading slash, replace remaining slashes with hyphens.
	s := cwd
	if len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	result := make([]byte, len(s))
	for i := range s {
		if s[i] == '/' {
			result[i] = '-'
		} else {
			result[i] = s[i]
		}
	}
	return string(result)
}

// writeSessionJSONL emits a minimal but structurally correct JSONL file
// that sessions.Open will index and Hydrate will count correctly.
//
// Structure per session:
//  1. A system_info attachment (for Open to find a first conversation line)
//  2. One user line (the first prompt)
//  3. One assistant line with usage + all tool_use blocks bundled under a
//     SINGLE message.id (so Hydrate's dedup math is correct — multiple
//     lines with the same message.id would be deduped, so everything must
//     live under one line).
//  4. Zero or more additional user lines (spec.prompts extra prompts, each
//     preceded by a tool_result user line to simulate real turn structure).
func writeSessionJSONL(path string, spec sessionSpec, cwd string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	baseTS := spec.started.UTC().Format(time.RFC3339Nano)
	laterTS := spec.updated.UTC().Format(time.RFC3339Nano)

	// Line 1: system_info attachment — gives Open a "hasAnyConvLine" hit.
	if err := enc.Encode(map[string]any{
		"type":      "attachment",
		"uuid":      spec.id + "-sys",
		"sessionId": spec.id,
		"timestamp": baseTS,
		"cwd":       cwd,
		"subtype":   "system_info",
	}); err != nil {
		return err
	}

	// Line 2: first user line.
	if err := enc.Encode(map[string]any{
		"type":      "user",
		"uuid":      spec.id + "-u0",
		"sessionId": spec.id,
		"timestamp": baseTS,
		"cwd":       cwd,
		"message":   map[string]any{"role": "user", "content": "initial prompt"},
	}); err != nil {
		return err
	}

	// Line 3: assistant line with usage + tool_use blocks (all one message.id).
	content := []any{
		map[string]any{"type": "text", "text": "response"},
	}
	// Build tool_use blocks: one block per invocation (repeat tool name N times).
	toolIdx := 0
	for toolName, count := range spec.tools {
		for i := 0; i < count; i++ {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    fmt.Sprintf("tu_%s_%d_%d", spec.id, toolIdx, i),
				"name":  toolName,
				"input": map[string]any{},
			})
			toolIdx++
		}
	}

	if err := enc.Encode(map[string]any{
		"type":      "assistant",
		"uuid":      spec.id + "-a0",
		"sessionId": spec.id,
		"timestamp": laterTS,
		"cwd":       cwd,
		"message": map[string]any{
			"id":    spec.id + "-msg0",
			"role":  "assistant",
			"model": spec.model,
			"content": content,
			"stop_reason": func() string {
				if len(spec.tools) > 0 {
					return "tool_use"
				}
				return "end_turn"
			}(),
			"usage": map[string]any{
				"input_tokens":               spec.usage.Input,
				"output_tokens":              spec.usage.Output,
				"cache_read_input_tokens":    spec.usage.CacheRead,
				"cache_creation_input_tokens": spec.usage.CacheCreation,
			},
		},
	}); err != nil {
		return err
	}

	// Extra prompt lines: each is a user line (not tool_result).
	for i := 0; i < spec.prompts; i++ {
		if err := enc.Encode(map[string]any{
			"type":      "user",
			"uuid":      fmt.Sprintf("%s-u%d", spec.id, i+1),
			"sessionId": spec.id,
			"timestamp": laterTS,
			"cwd":       cwd,
			"message":   map[string]any{"role": "user", "content": fmt.Sprintf("follow-up prompt %d", i+1)},
		}); err != nil {
			return err
		}
	}

	return nil
}
