// Package pr is a thin wrapper over the `gh` CLI that surfaces
// pull-request state to drive Canopy's operational view. One
// `gh pr list` call per repo yields the state for every branch; the
// aggregator looks up by HeadBranch.
//
// Failure modes (no gh binary, not authenticated, exec failure) map
// to sentinel errors so callers can degrade silently.
package pr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const ghPRListLimit = "100"

var ErrNoGH = errors.New("pr: gh CLI not installed")
var ErrNotAuthed = errors.New("pr: gh not authenticated")

// PRState is the lifecycle state of a pull request (gh's `state` field).
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateMerged PRState = "MERGED"
	PRStateClosed PRState = "CLOSED"
)

// CIStatus is the rolled-up CI status across all checks on a PR.
// The empty value means "no checks reported yet" — render as neutral,
// not as a failure.
type CIStatus string

const (
	CIPending CIStatus = "PENDING"
	CISuccess CIStatus = "SUCCESS"
	CIFailure CIStatus = "FAILURE"
)

// ReviewState is the rolled-up review decision (gh's `reviewDecision`).
// The empty value means "no review activity yet".
type ReviewState string

const (
	ReviewApproved         ReviewState = "APPROVED"
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewRequired         ReviewState = "REVIEW_REQUIRED"
)

// PR is the per-pull-request state surfaced by this package.
//
// State and IsDraft are kept separate: a draft PR is still OPEN, and
// draft-vs-ready treatment is orthogonal to the cleanup signal
// carried by State. CIRollup and ReviewState use the empty string for
// "no signal" — render as neutral, not as a failure.
type PR struct {
	Number      int
	Title       string
	HeadBranch  string
	State       PRState
	IsDraft     bool
	CIRollup    CIStatus
	ReviewState ReviewState
	MergedAt    time.Time // zero if not merged
	UpdatedAt   time.Time
	URL         string
}

// seamsMu guards lookPath and runCmd. The read paths are hot (List
// per repo), the write path is rare (test setup, demo wiring) —
// RWMutex is the right shape.
var seamsMu sync.RWMutex
var lookPath = exec.LookPath

// runCmd is the seam every external invocation in this package goes
// through. cmd.Output() leaves stderr on *exec.ExitError.Stderr when
// cmd.Stderr is nil, which isAuthErr relies on.
var runCmd = func(ctx context.Context, workingDir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workingDir
	return cmd.Output()
}

func getRunCmd() RunCmdFunc {
	seamsMu.RLock()
	defer seamsMu.RUnlock()
	return runCmd
}

func getLookPath() func(string) (string, error) {
	seamsMu.RLock()
	defer seamsMu.RUnlock()
	return lookPath
}

// RunCmdFunc is the shape of the exec seam swapped by SetRunCmd.
type RunCmdFunc func(ctx context.Context, workingDir, name string, args ...string) ([]byte, error)

// SetRunCmd replaces the package-level exec seam used by List. Returns the
// previous value so callers (tests, the demo subcommand) can restore it.
// Production code does not call this.
func SetRunCmd(fn RunCmdFunc) RunCmdFunc {
	seamsMu.Lock()
	defer seamsMu.Unlock()
	prev := runCmd
	runCmd = fn
	return prev
}

// LookPathFunc is the shape of the PATH-lookup seam swapped by SetLookPath.
type LookPathFunc func(name string) (string, error)

// SetLookPath replaces the package-level exec.LookPath seam used by List's
// gh-availability check. Returns the previous value so callers (tests, the
// demo subcommand) can restore it. Without this, swapping SetRunCmd alone
// isn't enough on hosts without gh installed: List would return ErrNoGH
// before reaching the run seam.
func SetLookPath(fn LookPathFunc) LookPathFunc {
	seamsMu.Lock()
	defer seamsMu.Unlock()
	prev := lookPath
	lookPath = fn
	return prev
}

// ghPR is the on-the-wire shape of one entry returned by
// `gh pr list --json …`. Field names mirror the gh JSON keys.
type ghPR struct {
	Number            int           `json:"number"`
	Title             string        `json:"title"`
	HeadRefName       string        `json:"headRefName"`
	State             string        `json:"state"`
	IsDraft           bool          `json:"isDraft"`
	StatusCheckRollup []ghCheckRoll `json:"statusCheckRollup"`
	ReviewDecision    string        `json:"reviewDecision"`
	MergedAt          string        `json:"mergedAt"`
	UpdatedAt         string        `json:"updatedAt"`
	URL               string        `json:"url"`
}

// ghCheckRoll mirrors one element of gh's statusCheckRollup array.
// Both Status and Conclusion are uppercase enum strings; the unknown
// cases are tolerated (treated as non-success, non-pending).
type ghCheckRoll struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// List runs the single canonical `gh pr list --json …` invocation,
// parses the result, and returns one PR per row. The shellout runs
// with cwd = repoRoot, which lets gh pick up the right remote without
// an explicit -R flag.
//
// Returns ErrNoGH when gh isn't on PATH, ErrNotAuthed when gh
// reports an auth failure on stderr, and a wrapped error for any
// other exec or parse failure.
func List(ctx context.Context, repoRoot string) ([]PR, error) {
	if _, err := getLookPath()("gh"); err != nil {
		return nil, ErrNoGH
	}

	args := []string{
		"pr", "list",
		"--json", "number,title,state,isDraft,headRefName,statusCheckRollup,reviewDecision,mergedAt,updatedAt,url",
		"--state", "all",
		"--limit", ghPRListLimit,
	}
	out, err := getRunCmd()(ctx, repoRoot, "gh", args...)
	if err != nil {
		return nil, classifyGHErr(err)
	}

	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("pr: parse gh output: %w", err)
	}

	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:      r.Number,
			Title:       r.Title,
			HeadBranch:  r.HeadRefName,
			State:       PRState(r.State),
			IsDraft:     r.IsDraft,
			CIRollup:    rollupCI(r.StatusCheckRollup),
			ReviewState: ReviewState(r.ReviewDecision),
			MergedAt:    parseGHTime(r.MergedAt),
			UpdatedAt:   parseGHTime(r.UpdatedAt),
			URL:         r.URL,
		})
	}
	return prs, nil
}

// gh check-run wire enums consumed only by rollupCI. Package-private —
// these are gh's API shape, not Canopy's domain.
const (
	ghCheckInProgress = "IN_PROGRESS"
	ghCheckQueued     = "QUEUED"
	ghCheckPending    = "PENDING"
	ghCheckSuccess    = "SUCCESS"
	ghCheckSkipped    = "SKIPPED"
	ghCheckNeutral    = "NEUTRAL"
)

// rollupCI collapses gh's statusCheckRollup array into a single
// CIStatus. Precedence rules:
//
//   - Empty array → "" (no checks attached; render as "no signal",
//     not as a failure).
//   - Any check still running (status IN_PROGRESS / QUEUED / PENDING)
//     → CIPending. Pending short-circuits over failures because a
//     pending check may still flip to success and the truthful
//     summary is "we don't know yet."
//   - Otherwise, if every conclusion is SUCCESS / SKIPPED / NEUTRAL
//     → CISuccess. SKIPPED and NEUTRAL are treated as non-failures
//     per the GitHub Checks API convention.
//   - Anything else (FAILURE, TIMED_OUT, CANCELLED, ACTION_REQUIRED,
//     STALE, or an unknown value) → CIFailure.
func rollupCI(checks []ghCheckRoll) CIStatus {
	if len(checks) == 0 {
		return ""
	}
	pending := false
	allOK := true
	for _, c := range checks {
		switch c.Status {
		case ghCheckInProgress, ghCheckQueued, ghCheckPending:
			pending = true
		}
		switch c.Conclusion {
		case ghCheckSuccess, ghCheckSkipped, ghCheckNeutral:
		case "":
			// Empty conclusion on a non-pending check is unexpected;
			// treat as non-success conservatively.
			if !pending {
				allOK = false
			}
		default:
			allOK = false
		}
	}
	if pending {
		return CIPending
	}
	if allOK {
		return CISuccess
	}
	return CIFailure
}

// parseGHTime parses an RFC3339 timestamp emitted by gh. Empty input
// (e.g. mergedAt on an open PR) maps to the zero time.
func parseGHTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// classifyGHErr maps a raw exec error from `gh` to a sentinel where
// possible. Only called when err is non-nil.
func classifyGHErr(err error) error {
	if isAuthErr(err) {
		return ErrNotAuthed
	}
	return fmt.Errorf("pr: gh invocation failed: %w", err)
}

// isAuthErr looks for any of the recognized auth-failure phrases in
// err.Error() or in the stderr captured on an *exec.ExitError.
// Matching is case-insensitive; the phrase list is intentionally
// short and broad.
func isAuthErr(err error) bool {
	probes := []string{
		"not logged in",
		"not logged into",
		"authentication required",
		"auth required",
		"please run: gh auth login",
	}
	hay := strings.ToLower(err.Error())
	for _, p := range probes {
		if strings.Contains(hay, p) {
			return true
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderr := strings.ToLower(string(exitErr.Stderr))
		for _, p := range probes {
			if strings.Contains(stderr, p) {
				return true
			}
		}
	}
	return false
}
