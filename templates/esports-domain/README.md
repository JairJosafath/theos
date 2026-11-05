# The esports domain

What an esports community app is about, for the agents that build and review
one: players and clubs who challenge each other, play, report what happened,
and climb ladders, leagues and tournaments. The practices that hold across
such apps, the mistakes that recur, and the controversies with the choice
this domain makes in each. Plain prose and no stack: the Standard has the
stack, this folder has the subject.

Read this file, then the topic file for the part you touch. The implementer
follows the practices and makes the listed choices unless the issue says
otherwise; the reviewer checks a change against the *Common mistakes* of
the topics it touches. These files belong to the factory: an agent reads
them and never edits them.

## Glossary

- **Player**: a person's competitive identity in the app: a display name
  (gamer tag), a region, the games they play. One account has one player.
- **Club** (team, org): players who compete together under one name, with an
  owner, staff roles and a **roster**, the players it fields.
- **Membership**: a player's place in a club, or a club's in a league, with
  its roles and the dates it began and ended. Memberships are history, not a
  flag.
- **Request** (invitation, application): one side asking another to make a
  membership: a club inviting a player, a player applying to a club, a
  league inviting a club. Pending until the other side answers.
- **Transfer**: a player leaving one club's roster for another's (or for
  none: a release, a retirement), as one change, within a league's
  **transfer window** where one applies.
- **Challenge**: one competitor (a player or a club) asking a named rival to
  play a match, with a game, a format and a proposed time. **Rival**: the
  competitor challenged; a **rivalry** is the record between two of them.
- **Match**: a game played between two competitors, from a challenge, a
  league fixture or a tournament bracket, in a **format** (best of one,
  best of three, a score limit).
- **Result report**: one side's account of a match: the score and who won.
  A result is **confirmed** when both reports agree, or when the other side
  lets the confirmation window pass; **disputed** when they differ.
- **Dispute**: a disagreement about a result, decided by a moderator from the
  reports and their evidence (screenshots, match IDs, recordings).
- **Forfeit**: a loss without playing, for a no-show or a withdrawal.
- **Rating**: a number estimating a competitor's strength from their
  confirmed results (Elo, Glicko-2, TrueSkill); with Glicko-2, a **rating
  deviation** says how sure the number is. **Provisional**: a rating from too
  few matches to rank on.
- **Leaderboard** (ladder, standings): competitors ordered by a rating or
  by points in a league or a season, with tie-breakers.
- **Season**: a dated period that a leaderboard covers, after which ratings
  may be softly reset.
- **League**: an organiser of clubs and players over seasons: members,
  fixtures, standings, moderators and news.
- **Tournament**: an event with a **format** (round robin, single or double
  elimination, Swiss), **seeding**, **check-in**, a **bracket** and rewards.
- **Moderator** (admin, staff): a person the league or the app trusts to
  decide disputes, act on reports and sanction players.
- **Smurfing**: a strong player on a new account to beat weaker ones.
  **Boosting**: someone else playing on an account to raise its rating.
  **Win trading**: rivals arranging results.

## Topics

One file per area; each has *Practices*, *Common mistakes*, *Controversies*
(the choice this domain makes and why) and *Sources*.

- `players-clubs-and-rosters.md`: players, clubs, roles, memberships,
  requests and transfers; who may act for a club.
- `challenges-and-matches.md`: a challenge's lifetime, scheduling across
  time zones, formats, no-shows and forfeits, rematches.
- `results-and-disputes.md`: two-sided result reports, confirmation and its
  window, evidence, disputes, corrections and the audit trail.
- `ratings-and-leaderboards.md`: rating systems, provisional ratings,
  recomputing from results, seasons and decay, leaderboards and
  tie-breakers.
- `leagues-and-tournaments.md`: league seasons and fixtures, tournament
  formats, seeding, check-in, brackets, byes, standings and rewards.
- `community-and-safety.md`: moderation, reports and sanctions, cheating,
  smurfing and win trading, minors, personal data, and posts.
