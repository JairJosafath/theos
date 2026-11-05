# Players, clubs and rosters

Who competes, under which name, and who may speak for a club. Most of what
goes wrong in a community app starts here: a club whose owner left, a player
on two rosters at once, or a request anyone could accept.

## Practices

- An account has at most one player. The player's display name is unique
  per app, case-insensitively and after Unicode normalization, so `Ace` and
  `ace` and a look-alike with a Cyrillic letter are one name. A rename keeps
  the player's identity and history; the old name is kept for a while so it
  cannot be taken to impersonate.
- A player's region and games are what matching and leaderboards filter on;
  they are a short list the app defines, not free text.
- A club has exactly one owner at all times. Ownership moves only by the
  owner handing it to another member who accepts, or by a moderator; an
  owner who deletes their account must hand it over first or the club is
  frozen, never ownerless.
- What a member may do in a club comes from their roles: owner, manager
  (roster and requests), captain (challenges and results for the club),
  member. Every action for a club checks the acting person's role in that
  club at that moment, on the server, on every request.
- A membership is a record with a start, an end and its roles, never a
  boolean. Leaving ends it; history (who played for whom, when) is kept and
  shown. Results and transfers point at memberships, so a past match still
  shows the roster that played it.
- A request (invite, application, league invite) has a sender, a receiver,
  a state (pending, accepted, declined, withdrawn, expired) and an expiry.
  Only the receiver, or someone holding the right role on the receiving
  side, accepts or declines; only the sender withdraws. Accepting is
  idempotent: a second accept changes nothing.
- A roster has the limits its league or game sets: a minimum to compete, a
  maximum size, at most one club per player per league (or per game, as the
  league says). The limits are checked when a membership is made, not later.
- A transfer is one change: the player's membership in the old club ends and
  the new one begins in the same transaction, with the reason (signing,
  loan, release, retirement) and the date. A loan has an end date and its
  return is the same kind of change.
- A league's transfer window is data on the league (open and close dates, in
  UTC); outside it, a transfer between the league's clubs is refused with
  the window's dates, while joining a club from no club may still be
  allowed, as the league says.

## Common mistakes

- Taking the acting club from the request body and trusting it, so a member
  of club A acts for club B by changing an ID (OWASP API1, broken object
  level authorization).
- A club with no owner after the owner left, so nobody can manage it, or two
  owners after a hand-over raced.
- A player on two rosters of the same league at once, because the old
  membership was not ended in the same transaction as the new one began.
- Storing "current club" on the player and losing who played for whom, so a
  past match shows today's roster.
- Accepting a request by its ID without checking the person accepting is its
  receiver, or letting a request be accepted after it expired or was
  withdrawn.
- Display names compared case-sensitively or before normalization, so
  impersonation by look-alike names is easy.
- Counters (member count, club count) stored and updated by hand next to the
  memberships, drifting from them; counts are computed, or kept in the same
  transaction as the change.

## Controversies

- **One club per player, or many?** A player may play several games, and
  some communities allow a player in a club per game. *The choice:* at most
  one club per player per league, and per game outside leagues. Why: it
  keeps rosters and transfers meaningful while allowing a player who plays
  two games two clubs.
- **Hard delete or keep a departed player?** *The choice:* deleting an
  account anonymizes the player ("deleted player") and keeps their
  memberships and results. Why: rivals' records, standings and other
  players' histories depend on those matches; personal data goes, the facts
  of who won stay (community-and-safety.md).
- **Who may sign a player: the manager alone, or the player too?** *The
  choice:* both: a club invites, the player accepts. Why: a roster that names
  someone who never agreed is a harassment and an integrity problem; a
  signing without the player's consent is never a transfer.
- **Free renames?** *The choice:* allowed, at most once per few weeks, with
  the old name reserved for a while and shown on the profile's history. Why:
  people change their tag, and rivals need to recognise who they played.

## Sources

- OWASP API Security Top 10 (2023), API1 Broken Object Level Authorization
  and API5 Broken Function Level Authorization:
  https://owasp.org/API-Security/editions/2023/en/0x11-t10/
- OWASP Authorization Cheat Sheet (deny by default, check on every request):
  https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html
- Unicode Technical Standard #39, Unicode Security Mechanisms (confusable
  detection for names): https://www.unicode.org/reports/tr39/
- Unicode Standard Annex #15, Unicode Normalization Forms:
  https://www.unicode.org/reports/tr15/
- FIFA Regulations on the Status and Transfer of Players: the registration
  periods (transfer windows) and loans that esports leagues commonly
  borrow, published under FIFA's legal documents.
