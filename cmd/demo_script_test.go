package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Tests for the cmd/demo_script.go directive parser and the small
// helpers around it. End-to-end script execution is covered by
// TestDemoScript_OpenPRWithPR in cmd/demo_test.go.

func writeTempScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "script.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

func TestParseScript_HappyPath(t *testing.T) {
	body := "" +
		"# Leading comment\n" +
		"width 140\n" +
		"\n" + // blank line
		"keys jjp\n" +
		"  wait 50ms  \n" + // surrounding whitespace
		"capture /tmp/frame.txt\n"
	got, err := parseScript(writeTempScript(t, body))
	if err != nil {
		t.Fatalf("parseScript: %v", err)
	}
	want := []scriptDirective{
		{line: 2, kind: dirWidth, arg: "140"},
		{line: 4, kind: dirKeys, arg: "jjp"},
		{line: 5, kind: dirWait, arg: "50ms"},
		{line: 6, kind: dirCapture, arg: "/tmp/frame.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("directives = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("directive[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseScript_EmptyFileReturnsNoDirectives(t *testing.T) {
	got, err := parseScript(writeTempScript(t, "\n# just a comment\n\n"))
	if err != nil {
		t.Fatalf("parseScript: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty script returned %d directives: %v", len(got), got)
	}
}

func TestParseScript_MissingFileError(t *testing.T) {
	_, err := parseScript("/definitely/not/a/script/file.txt")
	if err == nil {
		t.Fatal("parseScript on missing path returned nil err, want non-nil")
	}
}

func TestNamedKey_KnownKeys(t *testing.T) {
	tests := []struct {
		in   string
		want tea.KeyType
	}{
		{"enter", tea.KeyEnter},
		{"return", tea.KeyEnter},
		{"esc", tea.KeyEsc},
		{"escape", tea.KeyEsc},
		{"tab", tea.KeyTab},
		{"shift-tab", tea.KeyShiftTab},
		{"shifttab", tea.KeyShiftTab},
		{"up", tea.KeyUp},
		{"down", tea.KeyDown},
		{"left", tea.KeyLeft},
		{"right", tea.KeyRight},
		{"backspace", tea.KeyBackspace},
		{"space", tea.KeySpace},
		{"ctrl-c", tea.KeyCtrlC},
		{"ctrlc", tea.KeyCtrlC},
		// Case-insensitive
		{"ENTER", tea.KeyEnter},
		{"Esc", tea.KeyEsc},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := namedKey(tc.in)
			if !ok {
				t.Fatalf("namedKey(%q) ok=false, want true", tc.in)
			}
			if got != tc.want {
				t.Errorf("namedKey(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNamedKey_UnknownKey(t *testing.T) {
	if _, ok := namedKey("not-a-key"); ok {
		t.Error("namedKey('not-a-key') ok=true, want false")
	}
	if _, ok := namedKey(""); ok {
		t.Error("namedKey('') ok=true, want false")
	}
}

func TestParseInt(t *testing.T) {
	if got, err := parseInt("42"); err != nil || got != 42 {
		t.Errorf("parseInt('42') = (%d, %v), want (42, nil)", got, err)
	}
	if _, err := parseInt("not-a-number"); err == nil {
		t.Error("parseInt('not-a-number') err=nil, want non-nil")
	}
}

func TestWriteFrame_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "frame.txt")
	if err := writeFrame(path, "hello"); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("file content = %q, want 'hello'", string(got))
	}
}

func TestApplyDirective_UnknownReturnsError(t *testing.T) {
	s := &scriptState{width: 80, height: 24}
	err := applyDirective(s, scriptDirective{kind: "wat", arg: ""})
	if err == nil {
		t.Fatal("applyDirective(unknown) returned nil err")
	}
	if !strings.Contains(err.Error(), "unknown directive") {
		t.Errorf("err = %v, want 'unknown directive …'", err)
	}
}

func TestApplyDirective_InvalidDurationReturnsError(t *testing.T) {
	s := &scriptState{width: 80, height: 24}
	err := applyDirective(s, scriptDirective{kind: dirWait, arg: "not-a-duration"})
	if err == nil {
		t.Fatal("applyDirective(wait, invalid) returned nil err")
	}
}

func TestApplyDirective_UnknownNamedKeyReturnsError(t *testing.T) {
	s := &scriptState{width: 80, height: 24}
	err := applyDirective(s, scriptDirective{kind: dirKey, arg: "not-a-key"})
	if err == nil {
		t.Fatal("applyDirective(key, unknown) returned nil err")
	}
	if !strings.Contains(err.Error(), "unknown named key") {
		t.Errorf("err = %v, want 'unknown named key …'", err)
	}
}

func TestApplyDirective_InvalidIntForWidthReturnsError(t *testing.T) {
	s := &scriptState{width: 80, height: 24}
	err := applyDirective(s, scriptDirective{kind: dirWidth, arg: "wide"})
	if err == nil {
		t.Fatal("applyDirective(width, invalid) returned nil err")
	}
}
