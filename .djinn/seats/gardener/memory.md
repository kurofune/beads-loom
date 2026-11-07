# Memory

- Seat files (.djinn/seats/*/charter.md, duties.toml) come from djinn's templates via `djinn init --resync-seats` (the djinn repo's internal/djinn/seat/templates/<seat>/, absent here). A seat-rule bead lists no `touches`. Its design says to land it as the lead's attended edit or upstream, because a resync reverts a local-only edit.
- This repo has no docs/adr/ directory. DECISIONS.md `## Decided` lines cite code, beads and bd memories, not ADR files.
- The Claude project memory dir (~/.claude/projects/-Users-nathangross-Projects-beads-pm-tool/memory/) does not exist. ~/.codex/memories/ is empty (as of 2026-11-07).
- DECISIONS.md:22-24 cite bd memories `loom-orchestration-pattern` and `zsh-json-parsing-hazard-hit-2026-08-08`, which `bd memories` lacks. This is a stale pointer for tend-docs or the lead, not a tend-seats finding.
- tend-workshop: `djinn slow-watch` and `djinn dolt-tend` both exit 1 without doing work while a drain runs. Exit 1 means "go on / nothing failed" in the duty, so a run during a drain files nothing for those steps (as of 2026-11-07).
- scripts/sync-vendored-skills.sh does not exist here, so the vendored-skill step never runs. The stale-binary sensor always skips: the installed djinn is built from the djinn repo, not this one. CURRENT.md has no `Last verified` line, so stale-current skips too.
