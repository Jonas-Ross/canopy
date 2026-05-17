# Claude Code JSONL Session Schema

Reference for the `sessions` package parser. Reflects what `~/.claude/projects/*/*.jsonl`
actually contained on Jonas's machine as of 2026-05-17.

## 1. Provenance

- **Location**: `~/.claude/projects/<flattened-cwd>/<sessionId>.jsonl`. The directory
  name is the absolute project path with `/` replaced by `-` (leading `-` retained).
- **Subagent sub-tree**: when a session spawns Task-tool subagents, an inner directory
  is created: `<sessionId>/subagents/agent-<agentId>.jsonl`. Each subagent has its own
  JSONL file but shares the parent's `sessionId`.
- **Population sampled**: 403 JSONL files total (159 top-level + 244 subagent). 11 files
  read across 6 distinct projects; both very old (Apr 25, v2.1.119) and current
  (May 17, v2.1.143) files; sizes from 5 to ~580 lines.
- **CLI versions seen**: 2.1.119, 121, 123, 126, 128, 129, 137, 138, 139, 142, 143.
  Schema is stable across all of them; new event types appear additively.

## 2. File-level structure

- One JSON object per line. No header, no trailing summary.
- Lines are mostly chronologically ordered by `timestamp` but **not strictly**: ~1-2 %
  of adjacent pairs invert (sub-second concurrent writes within a turn). Parser must
  not assume monotonic timestamps; tie-break via `parentUuid` chain.
- One file = one `sessionId` (verified across all 403). Filename stem = `sessionId`.
- Three line flavors: **conversation** (`user`, `assistant`), **side-band**
  (`system`, `attachment`, `file-history-snapshot`), and **session-meta**
  (`permission-mode`, `ai-title`, `last-prompt`, `agent-name`, `custom-title`,
  `pr-link`, `queue-operation`, `worktree-state`, `agent-setting`). Meta lines have no
  `uuid` / `timestamp` and live outside the parent-chain — the CLI rewrites them as
  state changes.

## 3. Top-level types (counts across 403 files)

| `type` value             | Count  | Has `uuid` | Has `parentUuid` | In transcript? |
|--------------------------|-------:|:----------:|:----------------:|:--------------:|
| `assistant`              | 23 496 | yes        | yes              | yes            |
| `user`                   | 16 160 | yes        | yes (or null)    | yes            |
| `attachment`             |  7 125 | yes        | yes              | side-band      |
| `last-prompt`            |  1 065 | no         | no               | meta           |
| `permission-mode`        |    675 | no         | no               | meta           |
| `system`                 |    630 | yes        | yes              | side-band      |
| `file-history-snapshot`  |    627 | no         | no               | meta           |
| `queue-operation`        |    434 | no         | no               | meta           |
| `ai-title`               |    394 | no         | no               | meta           |
| `pr-link`                |    281 | no         | no               | meta           |
| `agent-name`             |    228 | no         | no               | meta           |
| `custom-title`           |     58 | no         | no               | meta           |
| `worktree-state`         |     34 | no         | no               | meta           |
| `agent-setting`          |      4 | no         | no               | meta           |

No `summary` type exists in any file (sketch-vs-reality mismatch — see §7).

## 4. Common fields on conversation/side-band events

```jsonc
{
  "uuid":          "<v4-uuid>",                  // unique per JSONL line
  "parentUuid":    "<v4-uuid>" | null,           // previous line in chain
  "sessionId":     "<v4-uuid>",                  // same on every line in file
  "timestamp":     "2026-05-17T07:42:45.048Z",   // RFC3339 with ms, always UTC
  "type":          "assistant",                  // discriminator (see §3)
  "isSidechain":   false,                        // true inside subagent files
  "userType":      "external",                   // always "external"
  "entrypoint":    "cli",                        // "cli" everywhere observed
  "cwd":           "/Users/jonasross/...",       // absolute path, may CHANGE mid-file
  "gitBranch":     "main" | "HEAD" | "",         // detached → "HEAD"; empty if not a repo
  "version":       "2.1.143",                    // CLI version that wrote the line
  "isMeta":        true | null,                  // true on synthetic local-command lines
  "isSidechain":   false                         // true on subagent threads
}
```

Optional fields: `requestId` (Anthropic API request id, on `assistant`), `promptId`
(groups user inputs from one submission), `slug` (kebab-case session label),
`agentId` / `teamName` / `attributionPlugin` / `attributionSkill` (provenance when a
skill/plugin/subagent generated the event), `sourceToolAssistantUUID` /
`sourceToolUseID` (back-links on tool-result `user` events).

`cwd` is **not** invariant: 9 of the sampled files had 2-3 distinct `cwd` values
(session `cd`'d into a subdir). The handoff's `CwdPrefix` correlation strategy
handles this.

## 5. Conversation events

### 5.1 `user`

```jsonc
{
  "type": "user",
  "message": {
    "role": "user",
    "content": "free-text prompt"          // OR an array of content blocks
  },
  "promptId": "...", "uuid": "...", "parentUuid": "..." | null,
  "isMeta": true | null,                   // true → synthetic local-command caveat
  "toolUseResult": { ... },                // present iff content carries a tool_result
  "sourceToolAssistantUUID": "<uuid>"      // present iff this is a tool result
}
```

`message.content` is a **string** for direct typed prompts (1 472 obs) and an **array**
for tool results / image pastes / interpolated commands (≫ 14 600 obs). Tool-results
ride as `type:"user"` because that's how the Anthropic API returns them.

### 5.2 `assistant`

```jsonc
{
  "type": "assistant",
  "message": {
    "id":          "msg_015FP92SgkbiF2qrcNgprmLg",
    "type":        "message",
    "role":        "assistant",
    "model":       "claude-opus-4-7",        // or claude-sonnet-4-6, claude-haiku-4-5-20251001, <synthetic>
    "content":     [ <content block>, ... ],
    "stop_reason": "tool_use" | "end_turn" | "stop_sequence" | null,
    "stop_sequence": null,
    "stop_details":  null,
    "usage":         { ... see §6 ... },
    "diagnostics":   null
  },
  "requestId": "req_011Cb7mM9gahZP4LwgnEcTU1"
}
```

**CRITICAL parser invariant**: when a single API response contains multiple content
blocks (e.g. `thinking` + `text` + `tool_use`), the CLI emits **one JSONL line per
block**, all sharing the same `message.id` and `requestId`, chained by `parentUuid`.
Each split copy carries the **same `usage`** object — naive summing will multi-count.
Dedupe token accounting by `message.id` (or `requestId`).

### 5.3 Content blocks

Inside `message.content` arrays:

| Block `type`    | Where     | Fields                                                          |
|-----------------|-----------|-----------------------------------------------------------------|
| `text`          | both      | `text`                                                          |
| `thinking`      | assistant | `thinking`, `signature`                                         |
| `tool_use`      | assistant | `id`, `name`, `input` (tool-defined object), `caller.type`      |
| `tool_result`   | user      | `tool_use_id`, `content` (string OR array of sub-blocks), `is_error` (optional, default false) |
| `image`         | user      | `source.type = "base64"`, `source.media_type`, `source.data`    |
| `tool_reference`| inside `tool_result.content` array | `tool_name` (compact summary blocks) |

`tool_result.content` is a string in 91 % of cases and an array in 9 %. When an array,
inner block types observed: `text` (834), `tool_reference` (1 011), `image` (14).

## 6. Token accounting (`message.usage` on `assistant`)

```jsonc
"usage": {
  "input_tokens":               6,
  "output_tokens":              210,
  "cache_creation_input_tokens": 14615,
  "cache_read_input_tokens":     22671,
  "cache_creation": {
    "ephemeral_1h_input_tokens": 14615,
    "ephemeral_5m_input_tokens": 0
  },
  "server_tool_use": {
    "web_search_requests": 0,
    "web_fetch_requests":  0
  },
  "service_tier":   "standard",
  "speed":          "standard",
  "inference_geo":  "",
  "iterations":     [ {... per-iteration usage ...} ]
}
```

These four fields satisfy a `TokenStats{Input, Output, CacheRead, CacheCreation}`:

| Go field         | JSONL path                              |
|------------------|-----------------------------------------|
| `Input`          | `message.usage.input_tokens`            |
| `Output`         | `message.usage.output_tokens`           |
| `CacheRead`      | `message.usage.cache_read_input_tokens` |
| `CacheCreation`  | `message.usage.cache_creation_input_tokens` |

## 7. Mapping to the v1 `sessions` sketch

| Sketch field                  | JSONL source                                                           | Notes |
|-------------------------------|------------------------------------------------------------------------|-------|
| `Event.SessionID`             | `.sessionId` (or filename stem)                                        | one per file |
| `Event.UUID`                  | `.uuid`                                                                | meta lines have none |
| `Event.ParentUUID`            | `.parentUuid`                                                          | null on session root |
| `Event.Timestamp`             | `.timestamp` (RFC3339 UTC, ms precision)                               | not strictly monotonic |
| `Event.Kind = User`           | `.type == "user"` and (string content **or** content array with no `tool_result`) | |
| `Event.Kind = Assistant`      | `.type == "assistant"` with text/thinking blocks                       | |
| `Event.Kind = ToolUse`        | `.type == "assistant"` with a `tool_use` content block                 | one JSONL line per block |
| `Event.Kind = ToolResult`     | `.type == "user"` with a `tool_result` content block                   | |
| `Event.Kind = Summary`        | **NO SOURCE**                                                          | see §8 mismatch |
| `AssistantMessage.Model`      | `.message.model`                                                       | |
| `AssistantMessage.Text`       | concatenation of `.message.content[].text` where block `type=="text"`  | |
| `AssistantMessage.StopReason` | `.message.stop_reason`                                                 | |
| `AssistantMessage.Tokens.*`   | `.message.usage.*` (see §6)                                            | dedupe by `message.id` |
| `ToolUse.ID`                  | block `.id`                                                            | |
| `ToolUse.Name`                | block `.name`                                                          | |
| `ToolUse.Input`               | block `.input` (raw JSON)                                              | |
| `ToolResult.ToolUseID`        | block `.tool_use_id`                                                   | |
| `ToolResult.IsError`          | block `.is_error` (default `false` if absent)                          | |
| `ToolResult.Content`          | block `.content` — string OR array (need flattening rule)              | |
| `Session.ID`                  | filename stem                                                          | |
| `Session.Path`                | file path                                                              | |
| `Session.Cwd`                 | first `.cwd` value seen (or latest, decide)                            | may change mid-file |
| `Session.Model`               | first non-`<synthetic>` `.message.model` seen                          | may mix in one file |
| `Session.StartedAt/UpdatedAt` | min / max `.timestamp` over file                                       | |

## 8. Open issues / schema risks

1. **`Summary` event kind has no JSONL source.** No `type:"summary"` exists. Drop
   `Summary` from `EventKind` or repurpose it for `system/compact_boundary`.
2. **`compact_boundary` events split a session into segments.** `system` with
   `subtype:"compact_boundary"` carries `compactMetadata` (`trigger`, `preTokens`,
   `postTokens`, `preservedSegment.{head,anchor,tail}Uuid`, `durationMs`). Expose so
   cost-over-time charts stay correct across compacts.
3. **Multi-event message splits double-count `usage`.** Dedupe token stats by
   `message.id` before summing. Naive sum was ~3× real on Opus turns sampled.
4. **`cwd` is not invariant within a file.** `Session.Cwd` is ambiguous — pick
   `first` / `most-frequent` / `latest` and document; index against the **set** of
   cwds for `CwdPrefix` correlation.
5. **Side-band line floods.** `attachment` (7 125), `stop_hook_summary` (355),
   `file-history-snapshot` (627). Filter these from the TUI hot path; expose via
   explicit `IncludeMeta` knob.
6. **`tool_result.content` polymorphism** (string vs array of `text`/`tool_reference`
   /`image` sub-blocks). Define a canonical flatten-to-string rule or store as `any`.
7. **`isMeta:true` synthetic user lines.** Local-command caveats and `/exit` output
   ride as `type:"user"` with `isMeta:true`. Default `Events()` should hide them.
8. **`<synthetic>` model name.** Two events use `model:"<synthetic>"`. Sentinel for
   CLI-generated (not Anthropic-generated) assistant text.
9. **Subagent files duplicate `sessionId`.** `<sessionId>/subagents/agent-*.jsonl` has
   the parent's `sessionId` and `isSidechain:true`. Discovery must walk the subtree or
   tool calls will appear orphaned. Subagent events add an `agentId` field.
10. **Meta-event lines lack `uuid` and `timestamp`.** All of `permission-mode`,
    `ai-title`, `last-prompt`, `pr-link`, `agent-name`, `custom-title`,
    `queue-operation`, `worktree-state`, `agent-setting`, `file-history-snapshot`.
    Surface as "session metadata", not events.
11. **Timestamp ordering is not strict.** ~1-2 % of adjacent pairs invert. Sort by
    `parentUuid` chain when order is load-bearing.
12. **Attachment/system subtype zoo.** 24 attachment subtypes (`hook_success`,
    `task_reminder`, `skill_listing`, `deferred_tools_delta`, `edited_text_file`,
    `diagnostics`, `command_permissions`, `auto_mode`, `plan_mode*`, …) and 8 system
    subtypes (`stop_hook_summary`, `turn_duration`, `compact_boundary`,
    `away_summary`, `local_command`, `api_error`, `informational`,
    `scheduled_task_fire`). Decide which to model in v1.

## 9. Sampling appendix

| File                                                                                                                                          | Lines |
|-----------------------------------------------------------------------------------------------------------------------------------------------|------:|
| `…/canopy/bbce9f2d-df7f-4a4d-8b9c-669fd6484232.jsonl`                                                                                          |    99 |
| `…/canopy/ca8582d2-ec55-41b6-8d2c-012302ab7f6d.jsonl`                                                                                          |    56 |
| `…/wiki-mcp/02ce0be3-82d5-4d68-b8ed-33fd368d419c.jsonl`                                                                                        |     5 |
| `…/wiki-mcp/1a88a454-1107-4a74-9532-c2dfdcdb4ac4.jsonl`                                                                                        |   121 |
| `…/issue-orchestrator/1388e405-4058-4b6f-8e8c-cc4930ee0fe3.jsonl`                                                                              |   581 |
| `…/issue-orchestrator/0677f0b5-0a72-40a3-bb51-c2de4d37af0a.jsonl`                                                                              |    11 |
| `…/issue-team/70de1bc5-cfad-4e9d-9fd1-ecf6b13bd5d1.jsonl`                                                                                      |   504 |
| `…/-Users-jonasross-dev-worktrees-wiki-mcp-issue-78/5237c1e4-b781-4805-8d83-75925f3002f8.jsonl`                                                |    27 |
| `…/wiki-mcp--claude-worktrees-feature-add-batch-variant-of-read-frontmatter/ea4f4dd1-…/subagents/agent-afdc0cd7a33ba0c59.jsonl` (subagent)     |    80 |
| `…/canopy/bbce9f2d-…/subagents/agent-af2ff91998cec0358.jsonl` (subagent)                                                                       |   177 |
| `…/-Users-jonasross-dev-worktrees-wiki-mcp-issue-62/8b5e939a-bec3-4abf-a3c8-ba57c8ffc40e.jsonl`                                                |   305 |

Global aggregations (all 403 files) used for the `type`-frequency table in §3 and the
content-block, attachment-subtype, model, and version tables.

## 10. Decisions locked in (post-audit, before M1)

Resolved with Jonas on 2026-05-17 after this schema audit. These supersede the
v1 sketch where they conflict.

| Sketch element | Decision | Rationale |
|---|---|---|
| `Session.Cwd string` | `Session.Cwds []string` populated at `Open()` from first + last conversation events (≤2 entries in ~all cases); `Hydrate()` fills the full distinct-cwd set in observation order. | Cwd is not invariant within a session (§4 / §8). Full-scan at Open() trashes lazy-hydration. First+last is ~95% correct for nearly-free; correlation hot path stays cheap. |
| `Session.Cwd` correlation lookup | Add `Store.SessionsByCwdPrefix(prefix string) []*Session`. Backed by an in-memory `[]struct{Cwd, SessionID string}` sorted slice; binary search for prefix. | Aggregator's hot path; O(log n + matches) instead of O(sessions) per snapshot. |
| `ToolResult.Content string` | `ToolResult.Content []ContentBlock` (tagged union: `text` / `tool_reference` / `image`). Add `ToolResult.String()` flatten helper that drops non-text blocks. | ~9 % of tool results are arrays of blocks, not strings (§8 item 7). Flattening on parse loses image/tool_reference data; a union is honest. |
| Subagent JSONLs | Each subagent file is its own `Session`. `Session.ID = "<parentSessionId>#<agentId>"`. Add `Session.IsSidechain bool` and `Session.ParentSessionID string`. `Sessions()` stays flat. | 244/403 files are subagents (§1). Flattening keeps forensics queries simple; parent linkage is queryable; ID collisions impossible. |
| `EventKind.Summary` | Drop. Add `EventCompactBoundary` with a `CompactMetadata` payload (`PreCompactTokens`, `PostCompactTokens`, `Trigger`). | No `type:"summary"` exists in any file. `compact_boundary` does, splits sessions pre/post-compact, and is load-bearing for accurate cost/time accounting (§8 item 1). |
| `TokenStats` aggregation in `Hydrate` | Dedupe by `message.id` before summing. Each line carries the full `usage` for its message; naive sums triple-count. | §8 item 2. |

### Pending (defer to M1 implementation, not API-shape)

- Timestamp ordering tie-break via `parentUuid` chain (§8 item 11).
- `<synthetic>` model sentinel handling.
- Attachment / system subtype filtering policy (§8 item 12) — which to surface, which to drop.
- Meta-line state-rewrite handling (`last-prompt`, `ai-title`, `pr-link`, etc.) — likely a `SessionMeta` struct exposed via `Session.Meta()`, populated on `Hydrate()`.
- File-position checkpointing for `Tail` (so restart picks up where we left off).

