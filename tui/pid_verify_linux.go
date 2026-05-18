//go:build linux

package tui

import (
	"os"
	"strconv"
	"strings"
)

// pidCwdMatches reports whether the process pid currently has a cwd
// that path-prefix-matches expected. Used to defend against PID reuse
// between aggregator's process list and the kill signal: if the PID
// has died and been recycled, its new cwd almost certainly won't
// match, and we skip the signal.
func pidCwdMatches(pid int, expected string) bool {
	if expected == "" {
		return false
	}
	cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return false
	}
	return cwd == expected || strings.HasPrefix(cwd, expected+"/")
}
