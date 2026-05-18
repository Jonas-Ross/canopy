<!--
Title: <type>(<scope>): <subject>   — Conventional Commits, imperative, lowercase.
                                      types: feat fix docs refactor test chore perf build ci

Branched off main. `/simplify` already run (skip for docs-only).
CI handles vet + build + race tests — don't restate it here.

Optimise for a 15-second read. Delete every section you don't need.
-->

## Summary

<!-- One or two sentences: what changed and *why*. Lead with the why if non-obvious.
     Link the issue if there is one: Closes #N. -->


## Heads-up

<!-- Look-here-first list for the reviewer: spec deviations, surprising choices,
     risky edges, before/after snippets for UI work. Anything the diff alone won't reveal.
     Delete the section if there's nothing to flag — silence beats padding. -->


## Not in this PR

<!-- Things deliberately deferred — name them so they don't read as oversights.
     Delete if empty. -->


## Verification

<!-- Only what was checked *beyond* CI's vet + build + race. e.g.:
       - `canopy demo --script=tui/testdata/scripts/<scenario>.txt` walks the new path
       - golden frames re-baked intentionally and eyeballed
       - manual smoke against a real repo with N worktrees
       - `--capture-png` visual check for the new glyph
     Skip the section if CI was all. -->
