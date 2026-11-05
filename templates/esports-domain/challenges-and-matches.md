# Challenges and matches

The challenge is the loop people come back for: name a rival, agree on a
time, play. It is also where an app gets spammed, gamed and confused by time
zones.

## Practices

- A challenge has a challenger, a challenged rival (a player or a club,
  never mixed: players challenge players, clubs challenge clubs), a game, a
  format (best of one, three or five), proposed times and a state: open,
  accepted, declined, withdrawn, expired, scheduled, played, cancelled. Each
  state change is a recorded event with who made it and when.
- Only the challenged side accepts or declines; only the challenger
  withdraws, and only before it is accepted. For a club, the person acting
  must hold a role that may (players-clubs-and-rosters.md).
- An open challenge expires after a set time without an answer; a scheduled
  match that nobody reports goes to the results' rules
  (results-and-disputes.md), never stays "scheduled" for ever. Expiry runs
  on the server, on a schedule, not when someone happens to open the page.
- Times are stored in UTC and shown in each viewer's own time zone, with the
  zone named. A proposal may offer several slots; accepting picks one. A
  scheduled match can be rescheduled only by both sides agreeing.
- One open challenge per pair of rivals at a time. A competitor has a cap on
  open challenges they have sent, and a cooldown after a declined one to the
  same rival, so challenges cannot be used to spam or harass.
- A challenge may restrict who can be challenged: within a rating range, in
  the same region, among players who opted in to open challenges. A player
  may turn incoming challenges off, or limit them to their friends or club.
- A match from a challenge, a league fixture or a tournament bracket is the
  same kind of record: two sides, a game, a format, a scheduled time, the
  rosters that played (the memberships at that time) and its result. One
  model of a match means one place for results, ratings and disputes.
- A no-show is a forfeit only after a grace period from the scheduled time,
  claimed by the side that showed up, with the evidence the app asks for; a
  side that withdraws after accepting forfeits.
- A rivalry, the record between two competitors (wins, losses, last
  meeting, streak), is computed from their confirmed matches, never stored
  by hand.

## Common mistakes

- Comparing a scheduled time with the server's local clock, or storing a
  time without its zone, so a match shows an hour off after a daylight
  saving change.
- Letting the challenger accept their own challenge, or anyone with a
  challenge's ID accept it.
- Expiring challenges only when someone loads the list, so the list shows
  stale challenges and the counts are wrong.
- No limit on challenges, so a player sends hundreds, or repeatedly
  challenges someone who declined.
- A different table and different rules for challenge matches, league
  fixtures and tournament matches, so a fix to results reaches one and not
  the others.
- Recording a forfeit the moment the scheduled time passes, so a late
  start by a minute loses the match.
- Mixing a player and a club as the two sides of a match, which no rating
  or leaderboard can count.

## Controversies

- **Ranked or casual challenges?** Rated challenges motivate; they also
  invite farming weak players. *The choice:* both, chosen per challenge;
  rated ones only between competitors within a rating range the app sets,
  and repeated rated matches between the same pair count less (or not at
  all) after a few in a period (ratings-and-leaderboards.md). Why: rivals
  should be able to play each other freely without that becoming the way to
  climb.
- **Who may challenge whom?** *The choice:* anyone may challenge a
  competitor who accepts open challenges; everyone else only from their
  friends, club or league. Why: open by default helps a new community find
  matches, and the opt-out keeps it from becoming harassment.
- **Accept with one tap or negotiate?** *The choice:* the challenger offers
  up to three slots and the rival picks one or proposes others once. Why: a
  back-and-forth thread stalls matches; one round of counter-proposal
  covers most cases.
- **Forfeit on a no-show automatically?** *The choice:* no: the side present
  claims it after a grace period of 15 minutes (a league may set its own),
  and the other side can dispute it. Why: automatic forfeits punish people
  for the app's clock and for the other side not pressing a button.

## Sources

- RFC 3339, Date and Time on the Internet: Timestamps:
  https://www.rfc-editor.org/rfc/rfc3339
- IANA Time Zone Database (named zones and their daylight saving rules):
  https://www.iana.org/time-zones
- OWASP API Security Top 10 (2023), API4 Unrestricted Resource Consumption
  (rate limits on actions such as challenges):
  https://owasp.org/API-Security/editions/2023/en/0xa4-unrestricted-resource-consumption/
- The Idempotency-Key HTTP header field (IETF HTTPAPI working group draft),
  for accepting a challenge or claiming a forfeit once:
  https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/
