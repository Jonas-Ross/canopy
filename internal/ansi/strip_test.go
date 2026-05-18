package ansi

import "testing"

func TestStrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no escapes", "hello world", "hello world"},
		{"single SGR", "\x1b[31mred\x1b[0m", "red"},
		{"multiple SGR", "\x1b[1;31mbold red\x1b[0m\x1b[32mgreen\x1b[0m", "bold redgreen"},
		{"non-SGR preserved", "\x1b]0;title\x07payload", "\x1b]0;title\x07payload"},
		{"empty SGR", "\x1b[mfoo\x1b[0m", "foo"},
		{"multi-line", "line1\n\x1b[36mline2\x1b[0m\nline3", "line1\nline2\nline3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Strip(tc.in); got != tc.want {
				t.Errorf("Strip(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
