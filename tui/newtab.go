package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
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
		return spawnKeystrokeTab("Ghostty", dir)
	case "WarpTerminal":
		return spawnWarpTab(dir)
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

// spawnWarpTab uses Warp's documented URI scheme so the new tab is launched
// with its cwd set by Warp itself — no keystroke timing race against zsh
// initialization. See github.com/warpdotdev/Warp issue #9083.
func spawnWarpTab(dir string) error {
	target := "warp://action/new_tab?path=" + url.QueryEscape(dir)
	return runCapturingStderr(exec.Command("open", target))
}

// spawnKeystrokeTab opens a new tab in appName by sending Cmd-T then typing
// the cd command. Used for terminals (Ghostty, Warp) that have no stable
// AppleScript verb for "new tab in cwd". Needs Accessibility permission
// for "System Events" on first run.
//
// The 0.4s delay after Cmd-T is to outwait zsh's startup so the prompt is
// rendered before we start typing — otherwise the `cd` keystroke arrives
// before the shell is ready and Return gets dropped on the floor. The
// shorter delay before Return is so it doesn't merge with the cd keystroke.
func spawnKeystrokeTab(appName, dir string) error {
	shellCmd := "cd " + shellSingleQuote(dir)
	script := `tell application ` + appleScriptString(appName) + ` to activate
delay 0.1
tell application "System Events"
	keystroke "t" using {command down}
	delay 0.4
	keystroke ` + appleScriptString(shellCmd) + `
	delay 0.1
	key code 36
end tell`
	return runCapturingStderr(exec.Command("osascript", "-e", script))
}

func spawnLinuxTab(dir string) error {
	// This is the last-resort fallback (we only get here when neither $TMUX
	// nor $TERM_PROGRAM identified the host). Try every candidate and only
	// give up once they've all failed — `wezterm cli spawn` returns a
	// connection error (not ErrNotFound) when wezterm is installed but no
	// GUI/mux is running, and the user is almost certainly in a different
	// terminal in that case.
	var lastErr error
	for _, cand := range []*exec.Cmd{
		exec.Command("wezterm", "cli", "spawn", "--cwd", dir),
		exec.Command("gnome-terminal", "--tab", "--working-directory="+dir),
		exec.Command("konsole", "--new-tab", "--workdir", dir),
	} {
		err := runCapturingStderr(cand)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("no supported linux terminal succeeded (last error: %w)", lastErr)
}

func runCapturingStderr(c *exec.Cmd) error {
	var stderr bytes.Buffer
	c.Stdout = io.Discard
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return cleanExecErr(err, stderr.Bytes())
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
