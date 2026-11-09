# CURRENT — Loom

**Mode:** post-launch.
**Goal:** keep Loom working for any beads user: public CI green, review follow-ups worked down.
**Last change:** filed 4 beads from the implicit-state-machine survey: loom-csz, loom-0mj, loom-3nh, loom-6p7.

## State

- Epics closed: Foundations (loom-3ft), Board (loom-yqh), Tracker Truth
  (loom-d1q), Initiatives (loom-g1v), Readiness (loom-iqo).
- `scripts/check-public.sh` and gitleaks (`make check-secrets`, full history, pinned in the Makefile) run in `make check` and CI.
- Public CI green from the first run (42178431854); CI runs Node 24 (vitest 5 and jsdom 30 need 22.22.2+, 24.15+ or 26+).
- Epic deferred: Runs (loom-0zj). Its external gate is gone; its work goes
  inside the djinn integration, whose backend seam now exists. loom-0zj.3 is live again.
- Graph: closed. Open spin-off: loom-pks (Initiatives chained count, P4).
- 6 `review-follow-up` beads open, all `proposed` (triage decides): loom-suh, loom-dxf (P2); loom-l3f, loom-k8j, loom-jy7, loom-ucz (P3, IMPORTANT findings left when a review ran out of attempts).
- State-machine beads (`state-machine` label, approved, ready): loom-csz project health state, loom-0mj run log race (P2); loom-3nh run transition table, loom-6p7 dialog pending flags (P3).
- loom-gkp (P2, `proposed`, in triage): an Enter that confirms an IME composition submits fields app-wide; only the title editor guards it.
- Settled surfaces, each ruled in DECISIONS.md — do not relitigate without new
  evidence: the 200-row ceiling (loom-jhd/7qh/nlm), Backlog paging by reachable
  rows (loom-rz0), the timeline reading `events` (loom-rs5), rollup cost
  rationed by refetch policy (loom-zx1), the sweep surface (loom-cfr), the
  board's focus-reveal geometry (loom-reg), the rollup partition (loom-8qt/1ya).
- Still standing, deliberately: the unbounded depth-50 CTE in
  `internal/queries/initiatives.go` (loom-zx1 counter-finding).

## Assumptions

- The summoner contract `internal/runs/` reads stays; nothing upstream retires it.
- The public repo carries the harness; djinn's footprint shrinks later through `djinn init` (a design note for the djinn session is written; not yet handed over).

## Open questions

- None open.

## Next checkpoint

Next: loom-csz, then loom-0mj; loom-gkp and the 6 follow-ups once triage approves each.

## Stop conditions

- The graph draws blocking only. Re-adding a whole-project canvas needs a new
  ruling, not a patch — DECISIONS.md 2026-08-16.
- A live test that FAILs against a reachable database is a fixture finding.
  Fix the seed or the assertion; never convert it back to a skip (AGENTS.md).
