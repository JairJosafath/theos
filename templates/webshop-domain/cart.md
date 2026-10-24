# Cart

The cart holds what a customer intends to buy before they check out. It is
the most-edited object in the shop and the least authoritative: prices,
stock and availability are looked up again at checkout, and nothing is
promised until the order is placed.

## Practices

- A cart has an owner: an account, or an anonymous session identified by an
  unguessable token in a cookie. A cart is never found by a sequential
  number. A cart's token in a URL or a log is a leak.
- A cart line is a variant and a quantity. The line keeps the price it was
  added at only for display ("was 10, now 12"); the cart's total is always
  recomputed from the current price rules, and the customer sees the change.
- Adding to the cart is idempotent per variant: adding the same variant
  again raises the quantity, it does not create a second line. Setting a
  quantity to zero removes the line. Quantity has a per-line maximum.
- The cart checks the variant is published and purchasable when a line is
  added and again at checkout; it shows the availability state, and it keeps
  a line whose stock ran out, marked, so the customer decides.
- An anonymous cart that survives login merges into the account's cart:
  lines added, quantities summed up to the maximum, the customer told. The
  anonymous cart is then gone.
- Carts expire: an anonymous cart after days, an account's after much longer,
  and expiry removes the cart and any reservation it held (stock.md). A cart
  that outlives its prices is recomputed, not trusted.
- The cart's totals (lines, shipping estimate, tax, discounts, grand total)
  come from one computation shared with checkout and the order, so the
  number the customer sees in the cart is the one on the order unless
  something changed, and the change is shown.
- A cart write is atomic: two taps on "add" or two tabs editing the same cart
  end with a consistent cart, never a lost update or a duplicate line.
  Updates carry the cart's version and a stale write is retried against the
  fresh cart.

## Common mistakes

- Finding a cart by a guessable identifier, so one customer can read or edit
  another's cart.
- Storing the price in the cart line and charging it at checkout, so a
  customer who waited a month pays last month's price, or a price fix never
  reaches open carts.
- Two "add to cart" requests making two lines, or a quantity update lost to a
  concurrent one.
- Merging on login by replacing one cart with the other, dropping what the
  customer had chosen on the other device.
- Reserving stock when an item is added to the cart without an expiry, so
  abandoned carts hold the stock forever.
- Computing the cart total in the storefront and sending it to the server,
  which then charges it.
- Letting the cart keep an unpublished or deleted variant, which then fails
  at payment with no explanation.
- Carts that never expire, so the table and the reservations grow without
  bound.

## Controversies

- **Reserve stock in the cart or at checkout?** Reserving at add-to-cart
  means a customer never loses an item they chose, at the cost of stock held
  by abandoned carts. *The choice:* reserve at the start of checkout, with a
  short expiry (minutes), and only check availability in the cart. Why: most
  carts are abandoned, and holding stock for them hurts every other customer;
  a reservation that lasts the checkout is what the customer experiences
  as "it was there when I paid."
- **Server-side cart or client-side cart?** A client-side cart (local
  storage) is fast and needs no session; a server-side cart survives devices
  and can be checked against stock and prices. *The choice:* server-side,
  with a session token for anonymous customers. Why: the cart is the start
  of an order, and the order needs a server-side truth; a client-side cart
  must be re-validated into a server-side one at checkout anyway.
- **Keep an out-of-stock line or drop it?** Dropping is tidy; keeping is
  honest. *The choice:* keep it, marked not purchasable and excluded from
  the total, and block checkout until the customer removes or changes it.
  Why: silently losing a line is the complaint "my item disappeared."
- **Price shown at add time or current price?** *The choice:* the current
  price, with the old price shown as a note when it changed. Why: the
  customer pays the current price in any case; showing another number in
  the cart only breaks trust at checkout.

## Sources

- Baymard Institute, cart and checkout usability research:
  https://baymard.com/research/checkout-usability
- OWASP Session Management Cheat Sheet (session identifiers):
  https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- Martin Fowler, Optimistic Offline Lock (versioned updates):
  https://martinfowler.com/eaaCatalog/optimisticOfflineLock.html
- Shopify, cart and checkout concepts:
  https://shopify.dev/docs/storefronts/headless/building-with-the-storefront-api/cart
