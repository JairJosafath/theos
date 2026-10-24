# Payments

The shop takes money through a payment provider and never touches the card.
What the shop owns is the link between an order and the provider's payment,
the handling of the provider's callbacks, and the money movements it records.
Everything here is about not charging twice, not losing a payment, and not
seeing a card number.

## Practices

- The card number, expiry and security code never reach the shop's servers,
  logs or database. The customer enters them on the provider's page, in the
  provider's hosted fields, or in its client library, and the shop receives
  a token or a payment identifier. The shop's compliance scope stays the
  smallest one (PCI DSS SAQ A).
- One payment attempt is one provider object (a payment intent, a session)
  created by the shop for one order, with the order's identifier in its
  metadata and the amount, currency and description from the order, never
  from the client. The order points at the attempt.
- Every call that creates or changes money at the provider carries an
  idempotency key derived from the shop's own fact (the order and the
  attempt), so a timeout and retry charges once.
- The webhook is the truth. The redirect back to the shop after payment is
  a hint to show a page; the order goes to paid only when the provider's
  signed webhook says so, and the handler verifies the signature before it
  reads the body. The handler is idempotent: the same event twice has one
  effect, and events may arrive out of order or late.
- The handler checks the webhook's amount and currency against the order
  before marking it paid; a mismatch is an incident, not a paid order.
- Authorization and capture are kept apart when the shop ships later or may
  ship partly: authorize at checkout, capture when the shipment is created,
  within the provider's authorization window. A shop that ships at once may
  capture immediately.
- Refunds are issued through the provider against the original payment,
  recorded on the order with the amount, the reason and who did it, and
  reflected in the order's state when the provider confirms them.
- A payment that stays pending (a bank transfer, a delayed method) keeps the
  order placed and the reservation held for a method-specific window; an
  expired attempt releases both and the customer may try again.
- The shop reconciles: every payment the provider reports has an order, and
  every paid order has a payment; the difference is a report someone reads.

## Common mistakes

- Marking the order paid on the redirect URL's success parameter, which
  anyone can open by hand.
- Accepting a webhook without verifying its signature, or verifying it after
  parsing and acting on the body.
- A webhook handler that is not idempotent, so a provider retry sends two
  confirmation e-mails or ships twice.
- Taking the amount to charge from the client, or from the cart instead of
  the placed order.
- Retrying a failed call to the provider without an idempotency key, so the
  customer is charged twice.
- Logging the request that carried a card number, or putting card data into
  an error report.
- Storing the full card number "for reorders" instead of the provider's
  token.
- Capturing on placement for an order that ships weeks later, past the
  window for a clean cancellation.
- Refunding by creating a new negative "payment" in the shop's own books
  without the provider's refund, so the money never moves.
- Treating a failed webhook delivery as lost: there is no job that replays
  the provider's events, so a down shop misses payments for good.

## Controversies

- **Hosted payment page or embedded fields?** The provider's page is the
  smallest compliance scope and the least design control; embedded fields
  look native and widen the scope a little. *The choice:* the provider's
  hosted page or its hosted fields (an iframe the provider serves), never
  the shop's own form for card data. Why: a shop that never sees the card
  has nothing to lose, and the conversion difference is small.
- **Capture now or at shipment?** *The choice:* authorize at checkout and
  capture when the shipment is created, falling back to immediate capture
  when the provider or the method does not support the delay. Why: a
  cancellation before shipment is then a void, not a refund, which is both
  cheaper and faster for the customer.
- **Save payment methods for one-click reorders?** It is a stated need of
  the reference webshop and a liability. *The choice:* yes, through the
  provider's saved-method tokens only, bound to the account, with the
  customer's consent and a way to remove them; the shop stores the token
  and the last four digits for display, nothing else. Why: the provider is
  the vault; the shop holds a pointer.
- **Which provider?** *The choice:* the provider is a setting of the shop,
  behind one interface (create an attempt, capture, refund, verify a
  webhook), with one implementation per provider. Why: it depends on the
  shop's country and contract; the interface keeps the order flow the same.

## Sources

- PCI Security Standards Council, PCI DSS v4 and the SAQ A scope:
  https://www.pcisecuritystandards.org/document_library/
- Stripe, webhooks and signature verification:
  https://docs.stripe.com/webhooks
- Stripe, idempotent requests: https://docs.stripe.com/api/idempotent_requests
- Stripe, separate authorization and capture:
  https://docs.stripe.com/payments/place-a-hold-on-a-payment-method
- Adyen, webhooks and the order of notifications:
  https://docs.adyen.com/development-resources/webhooks
- OWASP Logging Cheat Sheet (what never goes in a log):
  https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html
