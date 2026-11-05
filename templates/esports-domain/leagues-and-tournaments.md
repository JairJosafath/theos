# Leagues and tournaments

Leagues and tournaments organise matches people would otherwise arrange by
challenge: who plays whom, when, and what it is worth. Their rules must be
set before the first match and visible to everyone in them.

## Practices

- A league has members (clubs, or players for a solo game), moderators, its
  rules (roster limits, transfer window, confirmation window, no-show grace,
  tie-breakers) and seasons. The rules of a season are fixed when it starts;
  a change applies from the next season, or with every member told.
- A league season's fixtures are generated, not typed: a round robin by the
  circle method (each member meets every other once, or twice home and
  away), with a bye when the count is odd. Fixtures are matches like any
  other (challenges-and-matches.md); the season's standings are computed
  from their confirmed results.
- Standings award points per result as the league's rules say (for example
  3 for a win, 1 for a draw where draws exist, 0 for a loss, 0 and a
  penalty for a forfeit) and order by points, then the tie-breakers in the
  league's published order (head-to-head, game or round difference, games
  won, then a deciding match or a draw of lots).
- A tournament has a format (single elimination, double elimination, round
  robin groups, Swiss), a size limit, registration and check-in windows, a
  seeding method and rewards. Registration closes, check-in confirms who
  shows up, and the bracket is built from the checked-in entrants only.
- Seeding is by rating (or the organiser's manual order), and the bracket
  places seeds so the top seeds meet as late as possible (1 and 2 in
  opposite halves). With a field that is not a power of two, byes go to the
  top seeds in the first round.
- A bracket advances on confirmed results only: the winner moves on, the
  loser leaves (single elimination) or drops to the losers' bracket (double
  elimination). A disputed match holds its branch, not the whole
  tournament.
- Swiss rounds pair entrants with the same score who have not met, avoid
  repeat pairings, and give at most one bye per entrant; the number of
  rounds is set at the start from the field's size.
- A tournament's rewards (trophy, medal, title, points toward a ranking) are
  granted when the tournament ends, to the entrants its final standings
  name, and stay on their profiles and in the trophy room.
- An entrant that withdraws mid-event forfeits their remaining matches; in a
  bracket, their next opponent advances.

## Common mistakes

- Building a bracket from registrations instead of check-ins, so the first
  round is full of no-shows.
- Seeding that puts the two strongest entrants in the same half, or byes
  given at random.
- Advancing a bracket on a reported but unconfirmed result, then having to
  unwind a round when it is disputed.
- Fixtures created by hand with members missing or meeting twice.
- Changing the points or tie-breakers mid-season, silently, so a standing
  changes with no match played.
- A league or tournament model separate from challenges, with its own match
  and result tables (results-and-disputes.md says why that breaks).
- Standings stored and edited, instead of computed from results.

## Controversies

- **Single or double elimination by default?** *The choice:* double
  elimination for fields up to 32, single elimination above. Why: double
  elimination makes one bad match survivable and gives more matches per
  entrant, which a community app wants; above 32 it takes too long.
- **Swiss pairing rules: strict or simple?** The FIDE Swiss rules are
  detailed (colour allocation, floaters) and designed for chess. *The
  choice:* a simpler Swiss: pair within score groups by rating, avoid
  rematches, float the lowest-rated down when a group is odd. Why: the
  extra rules serve chess's colours, which esports matches mostly lack.
- **Seed by rating or by the organiser?** *The choice:* by rating by
  default, and the organiser may reorder before the bracket is published.
  Why: ratings are the fairest default, and organisers know about strength
  the ratings miss (a returning player).
- **Can a league change its rules mid-season?** *The choice:* only the
  schedule (a postponed fixture) and only with both sides told; points,
  tie-breakers and roster limits wait for the next season. Why: standings
  must mean what members signed up for.

## Sources

- FIDE Handbook, C.04 Swiss system rules and C.07 tie-break regulations:
  https://handbook.fide.com/
- Round-robin tournament, the circle method of scheduling:
  https://en.wikipedia.org/wiki/Round-robin_tournament
- Double-elimination tournament (bracket structure and the grand final):
  https://en.wikipedia.org/wiki/Double-elimination_tournament
- Esports Integrity Commission (ESIC), codes (competition integrity):
  https://esic.gg/codes/
