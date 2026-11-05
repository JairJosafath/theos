# Results and disputes

A result is the one fact everything else is built on: ratings, standings,
rivalries, rewards. The app rarely sees the game itself, so a result is what
two sides say happened, and the rules decide when that becomes true.

## Practices

- Each side reports once: the score per game of the format and who won. A
  report is made by someone who played or holds the role to report for
  their club, and only after the match's scheduled start.
- Two agreeing reports confirm the result. When only one side reports, the
  other has a confirmation window (a setting, 24 hours by default) to
  confirm or dispute; when it passes, the result is confirmed as reported.
  The window runs on the server, on a schedule.
- Two reports that differ make the match disputed. A disputed match counts
  for nothing (no rating, no standings, no rivalry) until a moderator
  decides it.
- A report may carry evidence: screenshots, a match ID from the game's own
  service, a recording link. The app keeps the evidence with the report and
  shows it to the other side and the moderator, never publicly by default.
- A moderator decides a dispute with a reason: confirm one side, set a
  result, order a replay, or void the match. The decision is recorded with
  who made it and why, and both sides see it. A moderator who plays in the
  match, or whose club does, does not decide it.
- A confirmed result is not edited. A correction (a moderator's late
  decision, a sanction for cheating) is a new event that supersedes the
  old one, and everything computed from results (ratings,
  leaderboards, rivalries) is recomputed from the corrected history
  (ratings-and-leaderboards.md).
- Scores are checked against the format: a best-of-three ends 2–0 or 2–1,
  never 3–1 or 1–1; a forfeit has no score, only a winner and a reason.
- Results that come from the game's own service (an API with match IDs) are
  the stronger evidence when the app has them; a report that contradicts the
  service is disputed automatically.

## Common mistakes

- One side reporting the result and that being final, so the loser never
  has a say, or the winner of a no-show writes any score they like.
- Letting anyone who knows the match's ID post a result, or a player report
  for a club they do not play for.
- A result that never confirms because the other side never answers: the
  match hangs, nobody's rating moves, and players learn that ignoring a loss
  works.
- Counting a disputed match in the standings or rating "for now", then
  reverting it, so the leaderboard jumps twice.
- Editing a confirmed result in place, losing what was reported and decided,
  with no way to recompute ratings correctly.
- A moderator deciding a dispute in their own club's match.
- Accepting scores the format cannot produce.

## Controversies

- **Trust the reports, or only the game's API?** *The choice:* reports by
  both sides, with the game's API as evidence where it exists. Why: most
  games give an app no reliable match API, and requiring one would exclude
  them; where it exists it settles most disputes before a person must.
- **Auto-confirm or require both sides?** *The choice:* auto-confirm a lone
  report after the window. Why: requiring both lets the losing side stall
  every result; the window and the right to dispute protect them.
- **Penalise false reports?** *The choice:* yes: a report a moderator finds
  false is a strike on the reporter (community-and-safety.md); a pattern
  leads to a sanction. Why: without a cost, disputing every loss is free.
- **Show evidence publicly?** *The choice:* no; to the two sides and the
  moderators only. Why: screenshots carry names, chats and sometimes other
  people's personal data.

## Sources

- Martin Fowler, Event Sourcing (results as an append-only history from
  which state is recomputed): https://martinfowler.com/eaaDev/EventSourcing.html
- Esports Integrity Commission (ESIC), codes (anti-corruption, code of
  conduct, disciplinary procedure): https://esic.gg/codes/
- OWASP Logging Cheat Sheet (an audit trail of who decided what):
  https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html
