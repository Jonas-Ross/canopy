package tui

import (
	"sort"

	"github.com/jonasross/canopy/procs"
)

// rankProcs returns a new slice ordered by interestingness for display in the
// detail pane. Three tiers: claude-family first, then known build/test tools,
// then everything else. Within a tier, higher pid wins — a weak "most recently
// started" proxy since procs.Process carries no start time.
func rankProcs(list []procs.Process) []procs.Process {
	if len(list) == 0 {
		return []procs.Process{}
	}
	out := make([]procs.Process, len(list))
	copy(out, list)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := procTier(out[i]), procTier(out[j])
		if ti != tj {
			return ti < tj
		}
		return out[i].Pid > out[j].Pid
	})
	return out
}

// procTier returns the rank tier of a process. Lower = more interesting.
func procTier(p procs.Process) int {
	switch {
	case isClaudeProc(p.Command, p.Args):
		return 0
	case isBuildToolProc(p.Command, p.Args):
		return 1
	default:
		return 2
	}
}

// buildToolBasenames is the starter set of build/test runners whose bare
// invocation is meaningful (npm run …, cargo build, etc.). Anything in this
// set qualifies regardless of argv.
var buildToolBasenames = map[string]bool{
	"cargo":  true,
	"make":   true,
	"pytest": true,
	"npm":    true,
	"pnpm":   true,
	"bun":    true,
}

// isBuildToolProc reports whether a process looks like an interactive build
// or test invocation worth surfacing above the long tail of infrastructure.
// `go` is special-cased: only `go test|build|run` qualifies, since bare `go`
// gets pulled in by gopls and other tooling that's not meaningful work.
func isBuildToolProc(cmd string, args []string) bool {
	if buildToolBasenames[cmd] {
		return true
	}
	if cmd == "go" && len(args) >= 2 {
		switch args[1] {
		case "test", "build", "run":
			return true
		}
	}
	return false
}
