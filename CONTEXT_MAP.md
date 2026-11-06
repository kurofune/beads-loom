# CONTEXT_MAP — Loom

Pointers only. The source file always beats this map.

## State files

| File | Holds |
|---|---|
| `AGENTS.md` | How to work here: build, test rules, git convention, profile |
| `CURRENT.md` | Live state — outranks every doc on "what are we doing now" |
| `DECISIONS.md` | Settled choices, open questions, dated log |
| `CONTEXT_MAP.md` | This file |

## Truth sources

| Question | File |
|---|---|
| What is Loom, how do I run it | `README.md` |
| Architecture, tech stack, phase plan | `docs/ARCHITECTURE.md` |
| bd / Dolt / summoner ground truth | `docs/ARCHITECTURE.md` §Verified ground truth |
| Wire contract (Go side) | `internal/httpapi` |
| Wire contract (TS side) | `web/src/api/types.ts` |
| Live-database test rule | `AGENTS.md` §Testing |
| Why the timeline reads `events`, not `dolt_diff_issues` | `docs/spikes/dolt-diff.md` |
| Per-bead work contract (djinn) | `AGENTS.md` §Per-Bead Work Contract |
| Task state, memories | `bd` (Dolt DB) — `bd list`, `bd memories` |

## Code

| Path | Holds |
|---|---|
| `internal/registry/` | Project registry: port/metadata resolution, pools, health |
| `internal/queries/` | Read layer — SQL only |
| `internal/bdexec/` | Write layer — every write shells out to `bd` |
| `internal/httpapi/` | chi routes, JSON codecs, CSRF guard |
| `internal/sse/` | Event hub, `dolt_hashof_db()` poller |
| `internal/runs/` | summoner child process, log tail, forensics parsers |
| `internal/sweep/` | Untracked-commit reconciliation |
| `internal/updates/` | Initiative target/nudge logic |
| `internal/loomtest/livedolt/` | The one live-environment test gate |
| `web/src/views/` | Backlog, Board, Detail, Runs, Now, Graph, Settings |
| `web/src/api/` | Typed client, TanStack Query hooks, SSE wiring |
| `web/src/keyboard/` | Shortcut registry + cheat sheet |

## Other docs

- `docs/screenshots/` — README images.
- `docs/spikes/` — timeboxed spike write-ups with their captures.
- `scripts/check-public.sh` — the personal-path guard `make check` runs.
