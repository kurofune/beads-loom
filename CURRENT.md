# CURRENT — Loom

**Mode:** post-launch.
**Goal:** keep Loom working for any beads user: public CI green, review follow-ups worked down.
**Last change:** loom-wzf landed: the Runs tab reads djinn's drain logs from the git common dir (and the old `.djinn/` dir) and streams new lifecycle lines live.

## State

- Epics closed: Foundations (loom-3ft), Board (loom-yqh), Tracker Truth
  (loom-d1q), Initiatives (loom-g1v), Readiness (loom-iqo).
- `scripts/check-public.sh` and gitleaks (`make check-secrets`, full history, pinned in the Makefile) run in `make check` and CI.
- Public CI green from the first run (42178431854); CI runs Node 24 (vitest 5 and jsdom 30 need 22.22.2+, 24.15+ or 26+).
- Epic deferred: Runs (loom-0zj). Its external gate is gone; its work goes
  inside the djinn integration, whose backend seam now exists. loom-0zj.3 is live again.
- Graph: closed. Open spin-off: loom-pks (Initiatives chained count, P4).
- No `review-follow-up` bead open. loom-4o2 (P2, open): discovery lists linked git worktrees as projects; config excludes `the-pit-*` until it lands.
- State-machine beads (`state-machine` label, approved, ready): loom-csz project health state, loom-0mj run log frozen past 3,000 lines (P2); loom-3nh managed run state in one place, loom-6p7 dialog pending flags (P3).
- Settled surfaces, each ruled in DECISIONS.md — do not relitigate without new
  evidence: the 200-row ceiling (loom-jhd/7qh/nlm), Backlog paging by reachable
  rows (loom-rz0), the timeline reading `events` (loom-rs5), rollup cost
  rationed by refetch policy (loom-zx1), the sweep surface (loom-cfr), the
  board's focus-reveal geometry (loom-reg), the rollup partition (loom-8qt/1ya).
- Still standing, deliberately: the unbounded depth-50 CTE in
  `internal/queries/initiatives.go` (loom-zx1 counter-finding).

## Assumptions

- The summoner contract `internal/runs/` reads stays. djinn now writes its drain logs under `<git common dir>/djinn/`; Loom reads both places.
- The public repo carries the harness; djinn's footprint shrinks later through `djinn init` (a design note for the djinn session is written; not yet handed over).

## Open questions

- None open.

## Next checkpoint

Next: loom-csz, then loom-0mj; loom-4o2.

## Stop conditions

- The graph draws blocking only. Re-adding a whole-project canvas needs a new
  ruling, not a patch — DECISIONS.md 2026-08-16.
- A live test that FAILs against a reachable database is a fixture finding.
  Fix the seed or the assertion; never convert it back to a skip (AGENTS.md).
