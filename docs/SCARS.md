# SCARS

UX failure patterns this repo has shipped before. Every item here is an
implicit acceptance criterion for any UI work — catch it this iteration, not
the next one.

- **Invisible affordances.** The staged-for-summon tray existed for four phases
  with no way to feed it except an undiscoverable drag side effect (loom-0n6).
  If a feature's only entry point is a side effect of another gesture, it does
  not exist. Every capability needs a visible, deliberate affordance.
- **Read-only fields that look editable.** The detail panel rendered four prose
  fields with no edit path and no signal that editing was impossible
  (loom-jx3/loom-pqn). If a field cannot be edited, either make it editable or
  make its read-only nature obvious.
- **Refetch under the user's hands.** SSE invalidation closing open menus /
  dropping optimistic edits (loom-323). Any new overlay or editor must engage
  the interaction lock.
