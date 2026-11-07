# Memory

## Triage notes
- Seat-file proposals that change an approval signal or the lead's own closing rule: flag changes-how-the-workshop-decides.
- A deletion the bead itself calls "no effect here today" is unreachable only by today's data: flag deleting-behaviour.
- `djinn triage decide` can run >10 min for one bead; run each in the background.
- Size-target proposals (garden:dolt-size): run `du -sm .beads/dolt/<db>/.dolt/*` before voting. `.dolt/git-remote-cache` (~288 MB here) is not compacted by `dolt gc`, so a whole-store size target can be unreachable by gc alone. loom-qhb was declined 1-2 on this.
