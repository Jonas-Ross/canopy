package sessions

import "encoding/json"

// rawMetaLine is the union projection of any JSONL meta line. Field
// tags match production schema sampled from ~/.claude/projects.
// Per-type fields stay at their zero value on lines of other types.
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
		meta.LastPrompt = raw.LastPrompt
	case "ai-title":
		meta.AITitle = raw.AITitle
	case "custom-title":
		meta.CustomTitle = raw.CustomTitle
	case "permission-mode":
		meta.PermissionMode = raw.PermissionMode
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
