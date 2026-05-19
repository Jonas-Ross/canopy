package sessions

import "encoding/json"

// metaUpdate is the result of parsing one JSONL meta line. Kind is the
// JSONL "type" value (e.g., "last-prompt"). Only the payload field
// matching Kind is meaningful; applyMetaUpdate dispatches on Kind.
type metaUpdate struct {
	Kind     string
	String   string // for last-prompt, ai-title, custom-title, permission-mode, agent-name, agent-setting
	PRLink   PRLinkMeta
	Worktree WorktreeStateMeta
	QueueOp  QueueOpMeta
}

// rawMetaLine is the union projection of any JSONL meta line. Field
// tags match production schema sampled from ~/.claude/projects. Per-
// type fields not used by the active Kind stay at their zero value.
type rawMetaLine struct {
	Type            string              `json:"type"`
	LastPrompt      string              `json:"lastPrompt"`
	AITitle         string              `json:"aiTitle"`
	CustomTitle     string              `json:"customTitle"`
	PermissionMode  string              `json:"permissionMode"`
	AgentName       string              `json:"agentName"`
	AgentSetting    string              `json:"agentSetting"`
	PRNumber        int                 `json:"prNumber"`
	PRURL           string              `json:"prUrl"`
	PRRepository    string              `json:"prRepository"`
	WorktreeSession *rawWorktreeSession `json:"worktreeSession"`
	Operation       string              `json:"operation"`
	Content         string              `json:"content"`
}

// rawWorktreeSession matches the inner worktreeSession object on
// worktree-state meta lines. Field tags match production data.
type rawWorktreeSession struct {
	OriginalCwd        string `json:"originalCwd"`
	WorktreePath       string `json:"worktreePath"`
	WorktreeName       string `json:"worktreeName"`
	WorktreeBranch     string `json:"worktreeBranch"`
	OriginalBranch     string `json:"originalBranch"`
	OriginalHeadCommit string `json:"originalHeadCommit"`
}

// metaTypes enumerates the JSONL meta line types that contribute to
// SessionMeta. file-history-snapshot is intentionally excluded: its
// payload is a snapshot blob, not last-write-wins state. Unknown types
// fall through silently for forward compatibility.
var metaTypes = map[string]bool{
	"last-prompt":     true,
	"ai-title":        true,
	"custom-title":    true,
	"permission-mode": true,
	"agent-name":      true,
	"agent-setting":   true,
	"pr-link":         true,
	"worktree-state":  true,
	"queue-operation": true,
}

// metaFromLine inspects one JSONL line. Returns (update, true, nil)
// when the line is a known meta type. Returns (_, false, nil) when the
// line is not meta (caller should treat it as a candidate event line).
//
// A JSON unmarshal error on a meta-looking line surfaces as
// (_, false, nil) too: the caller's downstream eventsFromLine pass
// will report the same malformed-line error with line-number context,
// which keeps error semantics consistent and avoids double-reporting.
func metaFromLine(line []byte) (metaUpdate, bool, error) {
	var raw rawMetaLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return metaUpdate{}, false, nil
	}
	if !metaTypes[raw.Type] {
		return metaUpdate{}, false, nil
	}
	return rawToMetaUpdate(&raw), true, nil
}

func rawToMetaUpdate(raw *rawMetaLine) metaUpdate {
	mu := metaUpdate{Kind: raw.Type}
	switch raw.Type {
	case "last-prompt":
		mu.String = raw.LastPrompt
	case "ai-title":
		mu.String = raw.AITitle
	case "custom-title":
		mu.String = raw.CustomTitle
	case "permission-mode":
		mu.String = raw.PermissionMode
	case "agent-name":
		mu.String = raw.AgentName
	case "agent-setting":
		mu.String = raw.AgentSetting
	case "pr-link":
		mu.PRLink = PRLinkMeta{
			Number:     raw.PRNumber,
			URL:        raw.PRURL,
			Repository: raw.PRRepository,
		}
	case "worktree-state":
		if raw.WorktreeSession != nil {
			mu.Worktree = WorktreeStateMeta(*raw.WorktreeSession)
		}
	case "queue-operation":
		mu.QueueOp = QueueOpMeta{
			Operation: raw.Operation,
			Content:   raw.Content,
		}
	}
	return mu
}

// applyMetaUpdate folds mu into meta with last-write-wins semantics
// per Kind.
func applyMetaUpdate(meta *SessionMeta, mu metaUpdate) {
	switch mu.Kind {
	case "last-prompt":
		meta.LastPrompt = mu.String
	case "ai-title":
		meta.AITitle = mu.String
	case "custom-title":
		meta.CustomTitle = mu.String
	case "permission-mode":
		meta.PermissionMode = mu.String
	case "agent-name":
		meta.AgentName = mu.String
	case "agent-setting":
		meta.AgentSetting = mu.String
	case "pr-link":
		meta.PRLink = mu.PRLink
	case "worktree-state":
		meta.WorktreeState = mu.Worktree
	case "queue-operation":
		meta.QueueOperation = mu.QueueOp
	}
}
