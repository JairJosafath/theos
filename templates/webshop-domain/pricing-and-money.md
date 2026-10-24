# Pricing and money

Money is where a rounding error becomes a complaint and a currency mix-up
becomes a loss. This topic is how an amount is stored and computed, how a
price becomes a line total with tax and a discount, and how that total is
frozen on the order.

## Practices

- An amount is an integer count of the currency's minor units (cents, pence,
  yen has none) together with its currency code (ISO 4217). Never a floating
  point number, never a decimal without a currency, never a string with a
  symbol.
- Two amounts are added, compared or summed only when their currencies
  match; a mismatch is a bug, not a conversion. Currency conversion, when
  the shop needs it, is its own step with a recorded rate and date.
- A price list is per currency: a variant has a price in each currency the
  shop sells in, set by the merchandiser, not computed from one price by a
  rate. A customer sees and pays in one currency for a whole order.
- Prices are tax-inclusive or tax-exclusive per shop (a setting), and the
  storefront says which. The tax is computed per line from the line's taxable
  amount and the rate that applies to that variant and destination, then
  rounded to minor units per line; the order's tax is the sum of the lines'
  tax. Rounding is half-up away from zero (or the mode the tax authority
  prescribes), stated once and applied everywhere.
- A unit price multiplied by a quantity is exact in integers; a percentage
  discount or a tax rate is applied once to the line amount and rounded once.
  When a discount must be split across lines, the remainder after dividing
  goes to one line (the largest), so the lines sum to the total.
- Discounts have a type (a percentage, a fixed amount, free shipping), a
  scope (a line, the order, shipping), conditions (a code, a minimum, a date
  range, a customer group), a maximum use, and never make a total negative.
  The applied discounts are recorded on the order by name and amount.
- Shipping is a line of its own with its own tax treatment, chosen at
  checkout from rates that depend on destination, weight or total.
- The order freezes the unit price, the line tax rate and amount, each
  discount and the totals as computed at placement
  (checkout-and-orders.md). An invoice shows those numbers; a later price
  change does not touch them. A refund is an amount in the order's currency,
  up to what was paid.
- Amounts are shown formatted for the customer's locale (separator, symbol
  position) from the integer and currency, at the edge; the shop's own
  records and interfaces carry the integer and the code.

## Common mistakes

- Floating point for money: 0.1 plus 0.2, a total that is off by a cent, a
  comparison that fails.
- Rounding the unit price and multiplying, or rounding each of many
  intermediate steps, so the lines do not sum to the total.
- Adding amounts in different currencies, or storing an amount without its
  currency and guessing it later.
- Computing one currency's price from another with a live rate, so the price
  changes between the listing and the cart.
- Applying a percentage discount to each line and rounding each, then
  showing a total discount that differs from the sum shown.
- A discount that makes a line or the order negative, or stacks with itself.
- Tax computed on the order total and then back-divided into lines, so the
  invoice's lines do not add up, or tax ignoring the destination.
- Tax-inclusive prices in the catalog and tax-exclusive maths at checkout, so
  the customer pays more than the page said.
- Showing a price in the customer's currency by converting at the edge.
- A refund larger than what was paid, or in a different currency.

## Controversies

- **Integer minor units or a decimal type?** A decimal type with a scale is
  also exact and reads naturally. *The choice:* integer minor units with a
  currency code, in storage, in the interfaces and in the computation. Why:
  every language and store has integers that behave the same way; decimal
  types differ in scale, rounding mode and serialization, and the provider's
  interfaces take minor units anyway.
- **Tax per line or on the total?** Authorities accept both with care; the
  two differ by cents. *The choice:* per line, rounded per line, summed.
  Why: the invoice must show tax per line in most jurisdictions, and lines
  that sum to the total is what a customer checks.
- **Tax-inclusive or tax-exclusive display?** *The choice:* a shop setting,
  tax-inclusive by default. Why: the reference shop sells to consumers in
  the EU, where the displayed price must be the final one; a shop elsewhere
  flips the setting.
- **One price list with conversion, or a price per currency?** *The choice:*
  a price per currency, merchandised. Why: a converted price moves, ends in
  odd cents and is wrong for the market; a merchandised price is a decision.
- **Rounding mode?** *The choice:* half-up away from zero unless the tax
  authority says otherwise, stated in one place. Why: it is what customers
  expect to see on a receipt; banker's rounding surprises them.

## Sources

- Martin Fowler, Money pattern (Patterns of Enterprise Application
  Architecture): https://martinfowler.com/eaaCatalog/money.html
- ISO 4217 currency codes and minor units:
  https://www.iso.org/iso-4217-currency-codes.html
- Stripe, currencies and zero-decimal currencies:
  https://docs.stripe.com/currencies
- European Commission, VAT rules for e-commerce (OSS, destination-based VAT):
  https://taxation-customs.ec.europa.eu/taxation/vat/vat-e-commerce_en
- Directive 98/6/EC on the indication of prices offered to consumers (the
  displayed price is the final price):
  https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=celex:31998L0006
