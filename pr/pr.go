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
	"time"
)

const ghPRListLimit = "100"

var ErrNoGH = errors.New("pr: gh CLI not installed")
var ErrNotAuthed = errors.New("pr: gh not authenticated")

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
	State       string    // OPEN / MERGED / CLOSED
	IsDraft     bool
	CIRollup    string    // SUCCESS / FAILURE / PENDING / "" (no checks)
	ReviewState string    // APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / ""
	MergedAt    time.Time // zero if not merged
	UpdatedAt   time.Time
	URL         string
}

var lookPath = exec.LookPath

// runCmd is the seam every external invocation in this package goes
// through. cmd.Output() leaves stderr on *exec.ExitError.Stderr when
// cmd.Stderr is nil, which isAuthErr relies on.
var runCmd = func(ctx context.Context, workingDir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workingDir
	return cmd.Output()
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

// List runs the single canonical `gh pr list --json …` invocation
// (documented in docs/handoff.md §"PR integration"), parses the
// result, and returns one PR per row. The shellout runs with cwd =
// repoRoot, which lets gh pick up the right remote without an
// explicit -R flag.
//
// Returns ErrNoGH when gh isn't on PATH, ErrNotAuthed when gh
// reports an auth failure on stderr, and a wrapped error for any
// other exec or parse failure.
func List(ctx context.Context, repoRoot string) ([]PR, error) {
	if _, err := lookPath("gh"); err != nil {
		return nil, ErrNoGH
	}

	args := []string{
		"pr", "list",
		"--json", "number,title,state,isDraft,headRefName,statusCheckRollup,reviewDecision,mergedAt,updatedAt,url",
		"--state", "all",
		"--limit", ghPRListLimit,
	}
	out, err := runCmd(ctx, repoRoot, "gh", args...)
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
			State:       r.State,
			IsDraft:     r.IsDraft,
			CIRollup:    rollupCI(r.StatusCheckRollup),
			ReviewState: r.ReviewDecision,
			MergedAt:    parseGHTime(r.MergedAt),
			UpdatedAt:   parseGHTime(r.UpdatedAt),
			URL:         r.URL,
		})
	}
	return prs, nil
}

// rollupCI collapses gh's statusCheckRollup array into a single
// status string. Precedence rules:
//
//   - Empty array → "" (no checks attached; render as "no signal",
//     not as a failure).
//   - Any check still running (status IN_PROGRESS / QUEUED / PENDING)
//     → "PENDING". Pending short-circuits over failures because a
//     pending check may still flip to success and the truthful
//     summary is "we don't know yet."
//   - Otherwise, if every conclusion is SUCCESS / SKIPPED / NEUTRAL
//     → "SUCCESS". SKIPPED and NEUTRAL are treated as non-failures
//     per the GitHub Checks API convention.
//   - Anything else (FAILURE, TIMED_OUT, CANCELLED, ACTION_REQUIRED,
//     STALE, or an unknown value) → "FAILURE".
func rollupCI(checks []ghCheckRoll) string {
	if len(checks) == 0 {
		return ""
	}
	pending := false
	allOK := true
	for _, c := range checks {
		switch c.Status {
		case "IN_PROGRESS", "QUEUED", "PENDING":
			pending = true
		}
		switch c.Conclusion {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// non-failure
		case "":
			// Empty conclusion on a non-terminal check is fine; it
			// will be covered by the pending branch. Empty on a
			// terminal check is unexpected but conservatively treated
			// as non-success.
			if !pending {
				allOK = false
			}
		default:
			allOK = false
		}
	}
	if pending {
		return "PENDING"
	}
	if allOK {
		return "SUCCESS"
	}
	return "FAILURE"
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
// possible. The auth-failure check uses a broad lowercase substring
// match against both err.Error() and any captured stderr — gh's
// wording has shifted across versions ("not logged into",
// "authentication required", "you are not logged in") and we don't
// want to chase the exact phrasing.
func classifyGHErr(err error) error {
	if err == nil {
		return nil
	}
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
