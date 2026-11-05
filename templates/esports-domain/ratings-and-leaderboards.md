# Ratings and leaderboards

A rating says how strong a competitor is; a leaderboard orders them. Both are
the numbers people care about most and trust least, so both must be
explainable from the results behind them.

## Practices

- A rating is computed from confirmed results only (results-and-disputes.md),
  per game and, where the app has them, per mode. Players and clubs have
  separate ratings; a club's is not the average of its players'.
- The rating system is Glicko-2 (see *Controversies*), with its rating,
  rating deviation (RD) and volatility per competitor, the system constant
  τ (tau) and the rating period as settings. A new competitor starts at the
  system's defaults (1500, RD 350), shown as provisional until their RD
  falls below a threshold; provisional competitors are left off the ranked
  leaderboard, not hidden from their own profile.
- Ratings are derived data. The history of confirmed results is the truth;
  ratings and leaderboards are recomputed from it, by one job, in the order
  matches were played (by their scheduled time, ties by confirmation time).
  A correction to an old result reruns the computation from that period on.
  The computation is deterministic: the same history gives the same numbers.
- Every rating change records the match and the before and after values, so
  a profile shows why a number moved.
- A leaderboard has a scope (the app, a region, a league, a club), a period
  (all time, a season), an ordering (rating, or points in a league) and
  tie-breakers in a fixed, published order. Two competitors who tie on
  everything share the rank (1, 2, 2, 4).
- A season is a dated period. At its end, the leaderboard is frozen as that
  season's final standings, and ratings are softly reset: moved part of the
  way back to the default and the RD raised, so the next season is open
  without erasing what was learned.
- Inactivity raises a competitor's RD (Glicko-2 does this per period
  without games), rather than lowering their rating; a leaderboard may
  require a minimum number of matches in the season to be ranked.
- Repeated rated matches between the same pair count less after a cap per
  period, so two rivals cannot farm each other's points.
- Leaderboards are read far more than they change: they are materialised by
  the ratings job after each run, not computed per page view, and the page
  says when they were last updated.

## Common mistakes

- Updating a rating in the request that confirms a result, in whatever
  order requests arrive, so two results confirmed together lose an update
  or apply out of order.
- Storing leaderboard rows by hand next to the results (the eprosports
  pattern of a leaderboard record per player), with nothing producing them,
  so they drift or stay empty.
- Ratings that cannot be recomputed because the inputs were not kept, so a
  voided match cannot be taken back.
- Ranking a player with two matches above everyone, because a provisional
  rating swung high.
- Floating point ratings compared for equality, or rounded differently on
  the page and in the ordering, so the displayed order looks wrong.
- Counting disputed or voided matches.
- Rating a player against a club, or across games.
- A hard reset each season that wipes skill information, so the first weeks
  of every season are mismatches.

## Controversies

- **Elo, Glicko-2 or TrueSkill?** Elo is simple and well known but has no
  measure of certainty, so new and returning players move too slowly or the
  constant K is tuned per case. TrueSkill handles teams of individuals and
  free-for-all but is patented and harder to explain. *The choice:*
  Glicko-2. Why: it carries certainty (RD) so provisional and inactive
  players are handled by the system, not by special cases; it is public and
  well documented; and head-to-head (one player or one club on each side) is
  the app's case.
- **Show the rating number, or only a tier?** *The choice:* both: the tier
  (bronze to champion, by rating bands) on profiles and the number on the
  leaderboard and the player's own page. Why: tiers motivate and hide
  small swings, and competitive players expect the number.
- **Recompute everything or update incrementally?** *The choice:* update
  incrementally per rating period, and recompute from the earliest changed
  period on a correction. Why: incremental runs keep it fast, and the full
  recompute path keeps corrections honest; both run the same code.
- **Decay ratings for inactivity?** *The choice:* no rating decay; RD grows
  instead, and the ranked leaderboard needs recent matches. Why: decay
  punishes a break and invites gaming the schedule; uncertainty is what
  actually grows.

## Sources

- Mark E. Glickman, Example of the Glicko-2 system (the algorithm, its
  defaults and the system constant τ): http://www.glicko.net/glicko/glicko2.pdf
- Mark E. Glickman, The Glicko system (rating deviation and inactivity):
  http://www.glicko.net/glicko.html
- Arpad E. Elo, The Rating of Chessplayers, Past and Present (Arco, 1978),
  the origin of the Elo system.
- Ralf Herbrich, Tom Minka, Thore Graepel, TrueSkill: A Bayesian Skill
  Rating System (NIPS 2006):
  https://www.microsoft.com/en-us/research/publication/trueskill-a-bayesian-skill-rating-system/
- FIDE Handbook, rating regulations and tie-break regulations:
  https://handbook.fide.com/
