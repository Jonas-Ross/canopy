package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openShellTab launches a shell in a new tab of the user's terminal emulator,
// cd'd to dir. It returns once the spawn command has exited — it does NOT
// wait for the spawned shell to exit. Errors describe why the spawn failed,
// not anything the user did in the shell afterward.
//
// Detection priority:
//  1. $TMUX — running inside tmux → `tmux new-window`
//  2. $TERM_PROGRAM — covers most macOS terminals (Apple Terminal, iTerm2,
//     WezTerm, ghostty, vscode/Cursor)
//  3. Linux fallback — try `wezterm cli`, `gnome-terminal`, `konsole` in turn
//
// If no supported terminal is detected the function returns an error so the
// TUI can show a notice instead of silently doing nothing.
func openShellTab(dir string) error {
	if os.Getenv("TMUX") != "" {
		return spawnTmuxWindow(dir)
	}

	switch os.Getenv("TERM_PROGRAM") {
	case "Apple_Terminal":
		return spawnAppleTerminalTab(dir)
	case "iTerm.app":
		return spawnITermTab(dir)
	case "WezTerm":
		return spawnWezTermTab(dir)
	case "ghostty":
		return spawnGhosttyTab(dir)
	case "vscode":
		// VS Code / Cursor's integrated terminal can't create another
		// integrated tab from inside one; punt to a Terminal.app window.
		if runtime.GOOS == "darwin" {
			return spawnAppleTerminalTab(dir)
		}
	}

	if runtime.GOOS == "linux" {
		return spawnLinuxTab(dir)
	}

	return fmt.Errorf("no supported terminal for new-tab (TERM_PROGRAM=%q)", os.Getenv("TERM_PROGRAM"))
}

func spawnTmuxWindow(dir string) error {
	return runQuiet(exec.Command("tmux", "new-window", "-c", dir))
}

func spawnAppleTerminalTab(dir string) error {
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application "Terminal"
	activate
	tell application "System Events" to keystroke "t" using {command down}
	delay 0.05
	do script ` + appleScriptString(shellCmd) + ` in front window
end tell`
	return runQuiet(exec.Command("osascript", "-e", script))
}

func spawnITermTab(dir string) error {
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application "iTerm"
	activate
	if (count of windows) is 0 then
		create window with default profile
	else
		tell current window to create tab with default profile
	end if
	tell current session of current window to write text ` + appleScriptString(shellCmd) + `
end tell`
	return runQuiet(exec.Command("osascript", "-e", script))
}

func spawnWezTermTab(dir string) error {
	return runQuiet(exec.Command("wezterm", "cli", "spawn", "--cwd", dir))
}

func spawnGhosttyTab(dir string) error {
	// Ghostty does not (yet) expose a stable AppleScript "new tab in cwd"
	// verb. Send Cmd-T to open a tab in the focused window, then write the
	// cd command into it via keystrokes. Requires Accessibility permission
	// for "System Events".
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application "Ghostty" to activate
delay 0.05
tell application "System Events"
	keystroke "t" using {command down}
	delay 0.1
	keystroke ` + appleScriptString(shellCmd) + `
	key code 36
end tell`
	return runQuiet(exec.Command("osascript", "-e", script))
}

func spawnLinuxTab(dir string) error {
	if _, err := exec.LookPath("wezterm"); err == nil {
		if err := runQuiet(exec.Command("wezterm", "cli", "spawn", "--cwd", dir)); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("gnome-terminal"); err == nil {
		return runQuiet(exec.Command("gnome-terminal", "--tab", "--working-directory="+dir))
	}
	if _, err := exec.LookPath("konsole"); err == nil {
		return runQuiet(exec.Command("konsole", "--new-tab", "--workdir", dir))
	}
	return fmt.Errorf("no supported linux terminal found (need wezterm, gnome-terminal, or konsole)")
}

// runQuiet runs c, discards stdout, captures stderr, and folds stderr into
// the returned error so the user sees the real reason a spawn failed.
func runQuiet(c *exec.Cmd) error {
	var stderr bytes.Buffer
	c.Stdout = io.Discard
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// shellSingleQuote wraps s in POSIX single quotes, escaping embedded quotes
// via the standard '\'' close-quote / escaped-quote / reopen-quote sequence.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptString returns s as a double-quoted AppleScript string literal
// with backslashes and double-quotes escaped.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
