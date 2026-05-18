package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openShellTab launches a shell in a new tab of the user's terminal emulator,
// cd'd to dir. It returns once the spawn command has exited — it does NOT
// wait for the spawned shell to exit.
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
		// VS Code / Cursor's integrated terminal can't open another tab in
		// itself; punt to the OS's native terminal. darwin returns here;
		// linux falls through to the spawnLinuxTab chain below.
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
	return runCapturingStderr(exec.Command("tmux", "new-window", "-c", dir))
}

func spawnAppleTerminalTab(dir string) error {
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application "Terminal"
	activate
	tell application "System Events" to keystroke "t" using {command down}
	delay 0.05
	do script ` + appleScriptString(shellCmd) + ` in front window
end tell`
	return runCapturingStderr(exec.Command("osascript", "-e", script))
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
	return runCapturingStderr(exec.Command("osascript", "-e", script))
}

func spawnWezTermTab(dir string) error {
	return runCapturingStderr(exec.Command("wezterm", "cli", "spawn", "--cwd", dir))
}

func spawnGhosttyTab(dir string) error {
	// Ghostty has no stable AppleScript "new tab in cwd" verb, so we send
	// Cmd-T then type the cd command via keystrokes. Needs Accessibility
	// permission for "System Events".
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application "Ghostty" to activate
delay 0.05
tell application "System Events"
	keystroke "t" using {command down}
	delay 0.1
	keystroke ` + appleScriptString(shellCmd) + `
	key code 36
end tell`
	return runCapturingStderr(exec.Command("osascript", "-e", script))
}

func spawnLinuxTab(dir string) error {
	for _, attempt := range []*exec.Cmd{
		exec.Command("wezterm", "cli", "spawn", "--cwd", dir),
		exec.Command("gnome-terminal", "--tab", "--working-directory="+dir),
		exec.Command("konsole", "--new-tab", "--workdir", dir),
	} {
		err := runCapturingStderr(attempt)
		if err == nil {
			return nil
		}
		// Only fall through to the next candidate when the binary itself is
		// absent. A real failure (e.g. wezterm running but no GUI server)
		// is the user's actual problem and should surface.
		if !errors.Is(err, exec.ErrNotFound) {
			return err
		}
	}
	return fmt.Errorf("no supported linux terminal found (need wezterm, gnome-terminal, or konsole)")
}

func runCapturingStderr(c *exec.Cmd) error {
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

// shellSingleQuote uses the POSIX '\'' close / escape / reopen trick so the
// path survives even if it contains a literal single quote.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
