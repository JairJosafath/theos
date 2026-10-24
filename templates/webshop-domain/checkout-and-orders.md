# Checkout and orders

Checkout turns a cart into an order: who buys, where it ships, how it is
paid. The order is the shop's durable fact; everything after it (payment,
shipment, refund, cancellation) is a new fact attached to it, never an edit.

## Practices

- Checkout is a sequence the customer can walk back through: contact,
  shipping address, shipping method, payment, review. Each step validates
  what it collected and saves it, so a reload or a lost connection does not
  start over. Guest checkout is offered; an account is an option after the
  order, not a gate before it.
- Placing an order is one atomic step on the server: re-validate the lines
  (published, purchasable, quantity within the maximum), recompute the
  prices and totals, confirm or take the reservations (stock.md), create the
  order with a unique order number, and then start the payment
  (payments.md). If any part fails, there is no order.
- The order freezes everything: each line's variant identifier and a copy of
  the name and the unit price as sold, the tax per line, the discounts
  applied, the shipping charge, the currency, the addresses, the totals.
  A later price or catalog change never alters a placed order.
- An order has a small, explicit set of states (placed, paid, fulfilled,
  partly or fully shipped, cancelled, refunded) and a history of transitions
  with who or what caused each. A state is derived from facts (a payment
  captured, a shipment sent), not set by hand.
- The order number the customer sees is not the database key: it is short,
  unique, hard to guess and carries no count of orders. The customer's order
  page is reachable only by the owner, or for a guest, by a link with an
  unguessable token sent to their e-mail.
- Placing an order is idempotent: the client sends a key for the attempt
  (the cart's identifier and version suffices), and a retry after a timeout
  returns the order already placed rather than placing a second one.
- The confirmation e-mail and the order page say the same thing as the
  order: lines, prices, totals, addresses, the payment state, and what
  happens next. They are sent after the order exists and the payment state
  is known, never before.
- A cancellation before shipment releases the stock and voids or refunds the
  payment; a return after shipment is a new fact with its own stock and
  money movement. An order is never deleted; the data protection path
  (accounts-and-personal-data.md) anonymizes it.

## Common mistakes

- Trusting totals, prices or discounts sent by the storefront at checkout.
- Two orders from one double-click or one retried request, each with its own
  payment.
- Creating the order after the payment succeeded, so a payment with no order
  exists when the request fails in between, and the customer paid for
  nothing the shop knows about.
- An order that points at the variant's current price, so the invoice
  changes when the price does.
- An order number that is a running count, telling competitors the shop's
  volume and letting a customer guess another's order.
- A state machine that allows "shipped" on an unpaid order, or "cancelled"
  on a shipped one, because the state is set by a form field.
- Sending the confirmation when the payment page is opened, before anything
  is paid, or not sending one when the payment is confirmed by webhook
  minutes later.
- Deleting cancelled orders, losing the audit trail the accounting needs.
- Requiring an account before the customer may see the total.

## Controversies

- **Order before payment or payment before order?** Some shops create the
  order only once the provider confirms payment, so there are no unpaid
  orders. *The choice:* create the order first, in state placed, then start
  the payment; the webhook moves it to paid. Why: the payment needs a
  stable reference to belong to, and an unpaid order that expires is easier
  to reason about than a payment that belongs to nothing. Unpaid orders
  expire with their reservations.
- **One page or many steps?** A single-page checkout is faster for an
  expert; steps are clearer on a phone and easier to resume. *The choice:*
  steps, saved as the customer goes, with an order review before the
  payment. Why: the state is recoverable at every step and each step has one
  thing to validate.
- **Edit a placed order or cancel and reorder?** Editing (change the
  address, add a line) is what customers ask for; it reopens prices, stock
  and payment. *The choice:* no edits to a placed order's lines or prices;
  the shipping address may be changed until the shipment is created; for
  everything else, cancel and reorder. Why: an immutable order is what
  accounting, returns and the payment reference rely on.
- **Guest checkout?** *The choice:* always offered, with an invitation to
  make an account after the order. Why: forced registration is the single
  largest cause of checkout abandonment in the published research.

## Sources

- Baymard Institute, checkout usability and abandonment:
  https://baymard.com/lists/cart-abandonment-rate
- Stripe, Payment Intents and the order-first flow:
  https://docs.stripe.com/payments/payment-intents
- Stripe, idempotent requests: https://docs.stripe.com/api/idempotent_requests
- Martin Fowler, Event Sourcing (facts over edits):
  https://martinfowler.com/eaaDev/EventSourcing.html
- OWASP, Insecure Direct Object Reference prevention (order pages):
  https://cheatsheetseries.owasp.org/cheatsheets/Insecure_Direct_Object_Reference_Prevention_Cheat_Sheet.html
