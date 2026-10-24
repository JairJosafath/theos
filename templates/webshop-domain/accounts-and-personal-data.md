# Accounts and personal data

A webshop holds names, addresses, e-mails, order histories and payment
pointers for real people. The law (in the EU, the GDPR) says what the shop
may keep and for how long, and the customer trusts the shop with a password.
This topic is how to take an account seriously and how to hold as little as
possible.

## Practices

- Guest checkout needs an e-mail and the addresses, nothing else; an account
  adds a password or a passwordless login and keeps the addresses and the
  order history. The shop collects what the order needs and asks for the
  rest only with a stated reason (a phone number for the carrier, say).
- Passwords are stored as a slow, salted hash (argon2id or bcrypt with a
  current cost), checked against a minimum length and a list of breached
  passwords, never against composition rules. Login is rate-limited per
  account and per address, with a constant-time response that does not say
  which of the e-mail and the password was wrong.
- A session is an unguessable token in a cookie marked for HTTPS only,
  unreadable by scripts and bound to the site, renewed on login and revoked
  on logout and on password change. A password reset is a single-use,
  short-lived link sent to the account's e-mail; the response to a reset
  request is the same whether the e-mail exists or not.
- Personal data has a purpose and a retention per purpose: the order and its
  invoice for as long as tax law requires (years), the account until the
  customer deletes it or after long inactivity, the cart for days, logs with
  personal data for weeks. Each is a setting with a default, and a job
  enforces it.
- The customer can see what the shop holds (a data export), correct it,
  delete the account, and withdraw consent, from their account page, without
  writing to anyone. Deleting an account anonymizes the orders (name,
  address, e-mail replaced by placeholders, the order and its amounts kept
  for the books) rather than deleting them.
- Consent is asked for what is optional (marketing e-mail, non-essential
  cookies), separately, unticked by default, recorded with a time, and
  withdrawable in one step. An order confirmation is not marketing and needs
  no consent.
- Logs and error reports carry identifiers (an account id, an order id), not
  the person: no e-mail, name, address, password, token or card data. The
  address and the e-mail are shown to staff who need them, and the access is
  logged.
- Personal data moves only to parties the shop has named (the payment
  provider, the carrier, the e-mail sender) under a processing agreement,
  and the privacy notice lists them.
- Children: the shop states a minimum age and does not knowingly make
  accounts for children under the applicable age.

## Common mistakes

- Requiring an account before checkout.
- Passwords hashed fast (plain SHA-256, MD5) or stored reversibly;
  composition rules ("one symbol, one digit") instead of length and a
  breach check.
- A login or reset form that reveals whether the e-mail exists.
- A session cookie readable by scripts or sent over plain HTTP, or one that
  survives a password change.
- Deleting an account by deleting its orders, breaking accounting, or
  refusing deletion because "the orders need it" instead of anonymizing.
- No retention: every cart, log and account ever, forever.
- A marketing checkbox pre-ticked, or one consent covering everything.
- E-mail addresses, names or whole request bodies in application logs and in
  error trackers.
- Giving the carrier or the e-mail service more fields than they need.
- Exporting a customer's data by hand on request, weeks later, because there
  is no self-service.

## Controversies

- **Passwords or passwordless (magic link, passkeys)?** Passwordless removes
  the password's risks and adds a dependency on the e-mail or the device.
  *The choice:* passwords with the practices above, and a passwordless
  option (e-mail link) next to them, with passkeys when the stack supports
  them. Why: customers expect a password to exist; the option lets the shop
  move without forcing anyone.
- **Anonymize or delete orders on account deletion?** *The choice:*
  anonymize the person on the order, keep the order. Why: tax law requires
  the invoice records for years; the GDPR's right to erasure yields to that
  obligation, and anonymizing satisfies both.
- **Keep order history for guests?** A guest's e-mail could link their orders
  into a history. *The choice:* no; a guest's orders are reachable by the
  tokened link in their e-mail only, and an account made later claims past
  orders only after the e-mail is verified. Why: a history keyed by e-mail is
  a leak to anyone who knows the e-mail.
- **How long to keep an inactive account?** *The choice:* a setting, with a
  default of a few years, and a warning e-mail before deletion. Why: the
  GDPR asks for no longer than necessary, and a shop knows its necessary.
- **Cookie banner?** *The choice:* none when the shop sets only the cookies
  it needs (session, cart, consent record); a real choice, not a banner with
  one button, when it adds analytics or marketing cookies. Why: strictly
  necessary cookies need no consent, and a banner that offers no choice is
  not consent.

## Sources

- Regulation (EU) 2016/679 (GDPR), articles 5 (principles), 6 (lawful
  basis), 7 (consent), 15 to 17 (access, rectification, erasure), 25 (data
  protection by design), 28 (processors):
  https://eur-lex.europa.eu/eli/reg/2016/679/oj
- European Data Protection Board, guidelines on consent and on data
  protection by design: https://www.edpb.europa.eu/our-work-tools/general-guidance/guidelines-recommendations-best-practices_en
- NIST SP 800-63B, Digital Identity Guidelines, authentication and lifecycle
  management (password rules, breach checks): https://pages.nist.gov/800-63-4/sp800-63b.html
- OWASP Password Storage Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
- OWASP Authentication Cheat Sheet (enumeration, rate limiting):
  https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- OWASP Session Management Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- Directive 2002/58/EC (ePrivacy), article 5(3), cookies and consent:
  https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=celex:32002L0058
