# The webshop domain

What a webshop is about, for the agents that build and review one: the
practices that hold across shops, the mistakes that recur, and the
controversies with the choice this domain makes in each. Plain prose and no
stack: the Standard has the stack, this folder has the subject.

Read this file, then the topic file for the part you touch. The implementer
follows the practices and makes the listed choices unless the issue says
otherwise; the reviewer checks a change against the *Common mistakes* of
the topics it touches. These files belong to the factory: an agent reads
them and never edits them.

## Glossary

- **Product**: what the shop sells, as the customer sees it on a page: a
  name, a description, images, a price.
- **Variant** (SKU, stock keeping unit): one sellable form of a product (the
  blue one in size M). Stock, price and the order line belong to a variant,
  never to a product.
- **Catalog**: every product and variant the shop may show, with their
  categories and attributes; what search and listing pages read.
- **Cart**: the variants a customer intends to buy, with quantities, before
  checkout. Owned by a session or an account; not an order.
- **Checkout**: the steps from a cart to a placed order: address, shipping
  choice, payment. Ends when the order is placed, not when it is paid.
- **Order**: the record of what was bought, at which prices, by whom, to be
  delivered where. Immutable once placed, save the shipping address until
  a shipment exists; later changes are new facts (a payment, a shipment, a
  refund, a cancellation).
- **Stock on hand**: the units physically in the warehouse. **Available
  stock**: on hand minus what is reserved for orders not yet shipped.
- **Reservation**: a hold on available stock for a cart or an order, with an
  expiry. **Oversell**: selling more than is on hand.
- **Payment provider** (PSP): the service that takes the card or the bank
  transfer so the shop never handles card data (Stripe, Adyen, Mollie).
- **Authorization**: the provider's promise that the funds are there.
  **Capture**: taking them. A capture may be partial and may come later.
- **Payment intent**: the provider's record of one attempt to pay for one
  order, which the shop's order points at.
- **Webhook**: the provider's call back to the shop when a payment changes
  state; the shop's source of truth for paid, failed and refunded.
- **Idempotency key**: a value the client sends with a request so a retry
  of the same request has the same effect once.
- **Minor units**: the smallest unit of a currency (cents, pence, yen);
  how money is stored and computed.
- **Tax-inclusive price**: a price that already holds the tax, shown as the
  customer pays it (the norm in the EU); **tax-exclusive**: tax added at
  checkout (the norm in the US).
- **Personal data**: anything about an identifiable person: a name, an
  address, an e-mail, an order history, an IP address.

## Topics

One file per area; each has *Practices*, *Common mistakes*, *Controversies*
(the choice this domain makes and why) and *Sources*.

- `catalog-and-search.md`: products, variants, categories, search and the
  listing pages; what a product page must get right.
- `cart.md`: the cart's lifetime, its owner, its prices, merging on login
  and what happens when stock or prices change under it.
- `checkout-and-orders.md`: the checkout steps, placing an order, the
  order's states and what may still change after it is placed.
- `stock.md`: on hand against available, reservations and their expiry,
  oversell, backorders and returns.
- `payments.md`: the provider redirect, idempotency, webhooks,
  authorization and capture, refunds; never a card number in the shop.
- `pricing-and-money.md`: integer minor units, currencies, tax, rounding,
  discounts and how a price is frozen on an order.
- `accounts-and-personal-data.md`: accounts and guest checkout, passwords,
  consent, retention and erasure under the GDPR, what to log and what never.
