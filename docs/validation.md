# Validation loop

Two complementary harnesses cover the TUI: text-mode goldens for layout
regressions (fast, deterministic, CI-gated) and a scripted demo subcommand
for full integration runs (catches wiring bugs the goldens can't see). PNG
rendering is opt-in for visual review.

## When to use what

| Concern | Tool | Why |
|---|---|---|
| Did a column move? Did a glyph change? Did the footer reflow? | `go test ./tui` (goldens) | Stripped-text frames diff cleanly; runs in <1s; CI catches drift. |
| Did `p` / `n` / `d` / `K` produce the right notice? Does the focused worktree highlight in the detail pane? | `canopy demo --script=…` | Exercises the full `cmd/root.go`-shaped pipeline against a sandbox. |
| Is the merge of styled spans actually visible (colour, bold, dim, italic)? | `canopy demo --script=… --capture-png=…` then read the PNG | Text strips style; pixels don't. |
| Quick eyeball — is this thing actually pleasant to look at? | `canopy demo` (no flags) | Interactive TUI against the sandbox. Safe — destructive ops are soft-gated. |

## The agent iteration loop

1. **Make the code change.**
2. **`go test ./... -race`.** If a golden under `tui/testdata/golden/` changed, read the diff. Decide whether the change is intentional. If yes:
   - `go test ./tui -update` to re-bake.
   - `git diff tui/testdata/golden/` to eyeball the new frames before committing.
3. **For wiring-shape questions** (aggregator config, env vars, subscribe → bridge → Update flow), use the demo subcommand:
   ```
   canopy demo --script=tui/testdata/scripts/<scenario>.txt
   ```
   The script's `capture` directives write text frames to the paths it names. Read the files.
4. **For visual-style questions** (was this rendered with the right colour? does the pulse glyph actually appear or is it a solid block?), add a `capture-png` directive and point it at a `.png`:
   ```
   capture-png /tmp/check.png
   ```
   then read the image. Requires `freeze` on PATH (see below).

## The demo subcommand

`canopy demo` builds a throwaway environment in `$TMPDIR/canopy-demo-*/`:

- A real `git init`-ed repo with main + four worktrees (`feat/auth`, `feat/dashboard`, `fix/login`, `chore/deps`) — covers ahead, behind, dirty, merged, closed, draft, and live-session states.
- A synthetic `~/.claude/projects` mirror with two JSONL session files: one within the 120s live window attributed to `feat/auth`, one outside it attributed to `chore/deps`.
- A `pr-fixture.json` covering OPEN / DRAFT / MERGED / CLOSED with the matching CI + review states.

It wires `CANOPY_DEMO=1`, swaps `pr.runCmd` to read the fixture JSON, opens the sessions store at the fixture root, and constructs the aggregator the same way `cmd/root.go` does. The TUI then runs (interactive) or the script replays (non-interactive).

### Soft-gates

`CANOPY_DEMO=1` short-circuits four destructive paths in `tui/ops.go`:

- `openURLCmd` returns success without invoking `open` / `xdg-open` / `cmd start`.
- `removeWorktreeCmd` returns success without running `git worktree remove --force`.
- `killProcsCmd` returns success without sending SIGTERM.
- `handleShellDrop` posts a "demo: would drop into shell at …" notice instead of `tea.ExecProcess`.

The sandbox already makes these safe — they'd act against the tmpdir. The soft-gate is belt-and-suspenders so a script that wandered out of the sandbox (someone hand-edits `CANOPY_SESSIONS_ROOT`, for example) still can't touch real state.

### Script grammar

One directive per line. `#` starts a comment. Blank lines are skipped.

| Directive | Argument | Effect |
|---|---|---|
| `width N` | int | Send `tea.WindowSizeMsg{Width: N, Height: 40}`. |
| `height N` | int | Send `tea.WindowSizeMsg{Width: 140, Height: N}`. |
| `keys STRING` | string | Send each rune as a `tea.KeyMsg{Type: KeyRunes}`. ASCII only. |
| `key NAMED` | enter, esc, tab, shift-tab, up, down, left, right, backspace, space, ctrl-c | Send the named key. |
| `wait DURATION` | Go duration (e.g. `100ms`, `2s`) | `time.Sleep` + flush any pending tea.Cmd. |
| `resolve` | — | Flush pending tea.Cmd without waiting. |
| `capture PATH` | path | Write the current `Model.View()` to PATH, ANSI-stripped. |
| `capture-png PATH` | path | Same, but pipe raw ANSI through `freeze` to produce a PNG. |
| `note TEXT` | string | Print `demo: TEXT` to stderr. Useful for orienting in logs. |

#### Cascade timing

After each key directive, any `tea.Cmd` returned from `Update` is *queued*, not run. It's flushed at the next `wait`, `resolve`, *or* the next key directive (which calls `flushPending()` first). End-of-script also flushes.

Consequence: a `capture` right after a key directive snapshots the *post-keypress, pre-cascade* state — the moment the notice is visible. That's exactly what catches wiring bugs (the PR cache miss in `cmd/root.go` surfaced as "no PR for feat/auth" at that snapshot point).

If you want the post-cascade state, add a `wait` (or `resolve`) between the keypress and the capture.

## Required tools

| Tool | When | Install |
|---|---|---|
| `go` 1.24+ | Always | system package manager / go.dev |
| `git` | Always (real `git init` in the fixture) | system package manager |
| `freeze` | Only for `capture-png` | `go install github.com/charmbracelet/freeze@latest` |

`freeze` is intentionally not a `go.mod` dep — it's a dev tool, not a runtime concern. Scripts that use `capture-png` fail loudly with the install hint when it's missing.

## Verifying a previously-seen bug class

To prove the loop catches the bug class it was built for, revert one of these and watch the failure surface:

| Revert | Caught by |
|---|---|
| Drop `PRCache: pr.NewCache(...)` from `cmd/demo.go` (or `cmd/root.go`) | `cmd.TestDemoScript_OpenPRWithPR` — the captured frame says "no PR for feat/auth" instead of "opening …". |
| Re-introduce `Background(colGreen)` on `livePulseStyle` in `tui/style.go` | `tui.TestGolden_PulseActive` — the SGR-42/102 guard in the test fires. (And `capture-png` of the pulse scenario shows a green block where the glyph should be.) |
| Revert `longestMatchingPath` filtering in `aggregator/loop.go` | `aggregator.TestSnapshot_NestedWorktreeSessionsAttributeToDeepest` — main shows a stray ●. |
