# Community and safety

An esports community is young, competitive and online, which makes it a
target for harassment, cheating and abuse of personal data. The app's rules
here protect people, not just the leaderboard.

## Practices

- Every user-facing action that can hurt someone (challenges, messages,
  posts, results, names, images) has a report button. A report names what it
  is about and why, goes to the moderators of the league (or the app's), and
  the reporter is told what was decided, without the details of a sanction.
- Moderation is roles, not people: an app's moderators, a league's
  moderators, a club's managers, each acting only within their scope, and
  every action recorded with who, what and why (results-and-disputes.md).
- Sanctions are graded and dated: a warning, a strike, a suspension from
  rated play or from a league for a period, a ban. A sanctioned player sees
  what for, until when, and how to appeal; an appeal is decided by a
  different moderator.
- Integrity: a player may play on one account only. Signals of smurfing
  and boosting (a new account beating established ones, a rating jump, logins
  shared between accounts, results always between the same few accounts) go
  to a moderator's queue; the app does not ban on a signal alone. Win
  trading and match fixing are sanctions after a moderator's finding, and
  the matches involved are voided and ratings recomputed.
- Minors: the app sets a minimum age (a setting; 13 by default, higher where
  local law asks), asks for a date of birth at sign-up, and does not let a
  person under the age of digital consent where they live use features that
  need consent (public profile, open messages) without a parent's consent.
  Messages between adults and minors who are not in the same club are off by
  default.
- Personal data is only what the app needs: a display name, a region, a
  date of birth (for the age rule; shown to nobody), an e-mail at the
  identity provider. Real names, locations and contacts are never shown
  unless the person turns them on. Erasure anonymizes the player and keeps
  the results (players-clubs-and-rosters.md).
- Posts (news, transfer announcements, roster changes) are written by a
  club's or league's staff, shown with their author, and taggable to the
  players, clubs, tournaments and transfers they are about; a tag is a
  link, never a copy of the thing tagged. User text is shown as text, never
  as HTML.
- Uploads (avatars, banners, evidence) are checked for type and size,
  re-encoded, stripped of metadata (EXIF locations), and served from a
  separate domain or path that runs no scripts.

## Common mistakes

- No way to report, or reports that disappear with no answer, so people
  leave instead.
- A league moderator able to sanction players in other leagues, or a
  moderator's action with no record of who did it.
- Banning automatically on an anti-cheat signal, with no person and no
  appeal.
- Showing a player's age, real name or the time zone of their location
  publicly, or keeping a photo's GPS metadata.
- Letting anyone message anyone, including adults messaging children.
- Rendering posts or names as HTML, so a crafted name runs script on every
  page it appears on.
- Treating moderators' decisions as editable, so a sanction can quietly
  vanish.

## Controversies

- **Require identity verification to compete?** *The choice:* no; one
  account per person by the terms, and integrity signals for a moderator to
  look at. A league may require more for its prize events. Why: verification
  excludes many honest players (especially minors) and collects sensitive
  data; most communities do not need it.
- **Public or private sanctions?** *The choice:* private to the player,
  with a public note only for bans in a league's competition ("removed for
  breaking rule 4"). Why: public shaming escalates harassment; competitors
  still need to know why a rival is gone.
- **Minimum age 13 or 16?** *The choice:* a setting, 13 by default, with the
  age of digital consent of the user's region applied to consent features.
  Why: the US COPPA line is 13 and the GDPR's age of consent is 13 to 16 by
  member state; a single fixed number is wrong somewhere.
- **Moderate before or after publishing?** *The choice:* after, with reports
  and fast removal, except for the first posts of new accounts, which wait
  for a moderator. Why: pre-moderation of everything stalls a community;
  new accounts are where spam and abuse start.

## Sources

- Regulation (EU) 2016/679 (GDPR), articles 5 (principles), 8 (a child's
  consent), 17 (erasure) and 25 (data protection by design):
  https://eur-lex.europa.eu/eli/reg/2016/679/oj
- Children's Online Privacy Protection Rule (COPPA), 16 CFR Part 312:
  https://www.ecfr.gov/current/title-16/chapter-I/subchapter-C/part-312
- UK Information Commissioner's Office, Age appropriate design code:
  https://ico.org.uk/for-organisations/uk-gdpr-guidance-and-resources/childrens-information/childrens-code-guidance-and-resources/
- Regulation (EU) 2022/2065 (Digital Services Act), article 16 (notice and
  action) and article 20 (complaints):
  https://eur-lex.europa.eu/eli/reg/2022/2065/oj
- Esports Integrity Commission (ESIC), codes (code of conduct,
  anti-corruption, disciplinary procedure): https://esic.gg/codes/
- OWASP Cross Site Scripting Prevention Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/Cross_Site_Scripting_Prevention_Cheat_Sheet.html
- OWASP File Upload Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/File_Upload_Cheat_Sheet.html
