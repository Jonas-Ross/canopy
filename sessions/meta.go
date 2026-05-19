package sessions

import "encoding/json"

// rawMetaLine is the union projection of any JSONL meta line. Tags
// match production schema sampled from ~/.claude/projects. Per-type
// fields stay at their zero value on lines of other types.
//
// The Legacy* fields cover an earlier CLI shape (still present in
// sessions/testdata/{projects/-tmp-projects-meta,events/-tmp-projects-events}
// and plausibly on disk for older sessions): `mode` instead of
// `permissionMode`, `title` instead of `aiTitle`/`customTitle`,
// `prompt` instead of `lastPrompt`. applyMetaLine prefers the
// production name and falls back to the legacy one.
type rawMetaLine struct {
	Type            string             `json:"type"`
	LastPrompt      string             `json:"lastPrompt"`
	AITitle         string             `json:"aiTitle"`
	CustomTitle     string             `json:"customTitle"`
	PermissionMode  string             `json:"permissionMode"`
	AgentName       string             `json:"agentName"`
	AgentSetting    string             `json:"agentSetting"`
	PRNumber        int                `json:"prNumber"`
	PRURL           string             `json:"prUrl"`
	PRRepository    string             `json:"prRepository"`
	WorktreeSession rawWorktreeSession `json:"worktreeSession"`
	Operation       string             `json:"operation"`
	Content         string             `json:"content"`

	LegacyMode   string `json:"mode"`
	LegacyTitle  string `json:"title"`
	LegacyPrompt string `json:"prompt"`
}

// rawWorktreeSession matches the inner worktreeSession object on
// worktree-state meta lines. Fields align with WorktreeStateMeta so
// the conversion in applyMetaLine is a plain type cast.
type rawWorktreeSession struct {
	OriginalCwd        string `json:"originalCwd"`
	WorktreePath       string `json:"worktreePath"`
	WorktreeName       string `json:"worktreeName"`
	WorktreeBranch     string `json:"worktreeBranch"`
	OriginalBranch     string `json:"originalBranch"`
	OriginalHeadCommit string `json:"originalHeadCommit"`
}

// applyMetaLine folds one JSONL line into meta with last-write-wins
// semantics. Returns true iff the line is a known meta type and was
// applied. Returns false for malformed JSON, unknown types, and any
// non-meta line — the caller (computeHydrate) handles those via the
// parallel eventsFromLine pass.
//
// file-history-snapshot is intentionally absent: its payload is a
// snapshot blob, not flat last-write-wins state.
func applyMetaLine(line []byte, meta *SessionMeta) bool {
	var raw rawMetaLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return false
	}
	switch raw.Type {
	case "last-prompt":
		meta.LastPrompt = firstNonEmpty(raw.LastPrompt, raw.LegacyPrompt)
	case "ai-title":
		meta.AITitle = firstNonEmpty(raw.AITitle, raw.LegacyTitle)
	case "custom-title":
		meta.CustomTitle = firstNonEmpty(raw.CustomTitle, raw.LegacyTitle)
	case "permission-mode":
		meta.PermissionMode = firstNonEmpty(raw.PermissionMode, raw.LegacyMode)
	case "agent-name":
		meta.AgentName = raw.AgentName
	case "agent-setting":
		meta.AgentSetting = raw.AgentSetting
	case "pr-link":
		meta.PRLink = PRLinkMeta{
			Number:     raw.PRNumber,
			URL:        raw.PRURL,
			Repository: raw.PRRepository,
		}
	case "worktree-state":
		meta.WorktreeState = WorktreeStateMeta(raw.WorktreeSession)
	case "queue-operation":
		meta.QueueOperation = QueueOpMeta{
			Operation: raw.Operation,
			Content:   raw.Content,
		}
	default:
		return false
	}
	return true
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
