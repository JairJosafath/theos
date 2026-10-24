# Stock

Stock is the count of what the shop can actually deliver. Two numbers
matter: on hand (in the warehouse) and available (on hand minus what is
promised). Almost every stock bug is treating one as the other, or changing
them without a record of why.

## Practices

- Stock is kept per variant and per location (one location is fine; the
  column is cheap and the migration later is not). On hand and reserved are
  stored; available is computed as on hand minus reserved, never stored as
  its own editable number.
- Every change to stock is a movement with a reason and a reference: a
  delivery received, a reservation made or released, an order shipped, a
  return accepted, a stock count correction. The current numbers are the sum
  of the movements; a correction is a new movement, not an edit.
- A reservation holds available stock for a checkout or a placed order and
  has an expiry. Taking a reservation checks available stock and decrements it
  in one atomic step (a conditional update: only when enough is available),
  so two customers cannot both get the last unit.
- When the order ships, the reservation becomes a decrement of on hand. When
  the order is cancelled or the checkout expires, the reservation is
  released. A sweeper releases expired reservations; nothing waits on it to
  sell.
- Oversell is a decision, not an accident: a variant may be marked
  back-orderable with a limit, and then available may go negative down to
  that limit, shown to the customer as a back-order with a date. A variant
  not so marked never sells below zero.
- The storefront shows availability as a state (in stock, low stock, out of
  stock, back-order), not the count, and the thresholds are settings.
- Receiving goods and counting stock are operations with their own records;
  a stock count that differs from the books produces a correction movement
  with the counted value, the difference and who counted.
- A returned item goes back to stock only when accepted as sellable; a
  damaged return is a movement to a non-sellable state, not a silent loss.

## Common mistakes

- Checking available stock and then decrementing it in two steps, so two
  checkouts in the same second both succeed on the last unit.
- Decrementing on hand when the order is placed, so an unpaid or cancelled
  order "loses" the unit until someone notices.
- Reservations without expiry, so abandoned checkouts hold stock until a
  manual cleanup.
- Editing the stock number in place to fix a discrepancy, with no record of
  the old value or the reason.
- Showing "in stock" from a cached count after the last unit sold.
- Letting the cart hold a reservation (cart.md) and then also reserving at
  checkout, double-counting the hold.
- Treating a return as a restock before anyone has looked at the item.
- Keeping stock in the catalog row next to the description, so a product
  edit and a sale race on the same row.

## Controversies

- **Reserve or first-come at payment?** Some shops take no reservations and
  decrement when the payment is confirmed; whoever pays first gets it, and
  the loser is refunded. *The choice:* reserve at the start of checkout with
  a short expiry, confirm the reservation when the order is placed, and
  decrement on hand when it ships. Why: refunding a customer who thought
  they had bought is the worst experience in the shop; a few minutes' hold
  is cheap.
- **Allow oversell?** Never overselling loses sales on items that are
  about to be restocked; always allowing it ships late. *The choice:*
  oversell only for variants explicitly marked back-orderable, up to a
  limit, shown as a back-order with a date. Why: it is the merchandiser's
  call per item, and it must be visible to the customer before they pay.
- **Store the available count?** A stored available count is quick to read
  and drifts. *The choice:* store on hand and reserved, compute available.
  Why: two stored numbers that must agree will not; the computation is
  trivial and the index on the row is enough.
- **Hard delete movements after a period?** *The choice:* keep them,
  archived if needed, with a snapshot per period for speed. Why: the
  movements are the audit trail accounting asks for.

## Sources

- Martin Fowler, Accounting Patterns (movements, not balances):
  https://martinfowler.com/apsupp/accounting.pdf
- Amazon DynamoDB developer guide, conditional writes (the pattern for an
  atomic decrement, whatever the store):
  https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/WorkingWithItems.html#WorkingWithItems.ConditionalUpdate
- PostgreSQL documentation, transaction isolation and the lost update:
  https://www.postgresql.org/docs/current/transaction-iso.html
- Shopify, inventory concepts (available, committed, on hand):
  https://help.shopify.com/en/manual/products/inventory/getting-started-with-inventory/inventory-states
