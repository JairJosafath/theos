# Catalog and search

The catalog is what the shop may sell: products, their variants, categories
and attributes. Search and the listing pages read it; the cart, stock and
orders point into it by variant. Everything a customer sees before the cart
is this topic.

## Practices

- A product is what the customer browses; a variant is what they buy. Price,
  stock, images that differ per colour, and the order line all hang off the
  variant. A product with one form still has exactly one variant, so the
  rest of the shop never special-cases it.
- A variant has a stable identifier that never changes and is never reused,
  separate from any human-facing code the merchandiser may rename. Orders,
  reservations and analytics keep that identifier.
- The catalog has a published state per product and per variant. Unpublished
  or discontinued items are not shown, not searchable, and cannot be added
  to a cart, but an old order still resolves them by identifier, so they are
  hidden, never deleted.
- A listing page shows the price the customer will pay in their currency and
  tax mode (pricing-and-money.md), the availability as a state (in stock,
  low, out of stock, back-orderable; stock.md), and whether the item is
  purchasable, from the same data the cart uses, so a customer never sees a
  price or availability the cart then contradicts.
- Search is forgiving: case-insensitive, tolerant of accents and plurals, with
  a sane result for an empty or very long query. A zero-result page offers a
  way out (popular items, categories, a spelling suggestion), never a blank.
- Listing and search results are paginated and sorted deterministically, with
  a tie-breaker on the identifier, so a customer paging forward never sees an
  item twice or misses one. Filters (facets) that would yield zero results are
  shown as such or hidden, not left as a trap.
- Product data a customer reads (name, description, attributes) is the
  merchandiser's text, shown as text: escaped, never rendered as markup unless
  the field is declared rich text and sanitized.
- A product page has a canonical URL that survives a rename; slugs that
  change keep a redirect. The page carries structured data (name, price,
  availability, currency) so search engines and comparison sites read what
  the customer reads.
- Images come with alt text from the catalog, in sizes fit for the listing,
  the product page and the zoom, and the page states its dimensions so the
  layout does not jump while they load.

## Common mistakes

- Pricing or stocking the product instead of the variant, then adding
  variants later and migrating every order line.
- Reusing a SKU for a new item after the old is discontinued, so old orders
  and reports name the wrong thing.
- Deleting a product that an order references, which breaks the order page,
  the invoice and the return.
- Showing a price on the listing computed one way and in the cart another
  (different rounding, a stale cache, a different tax mode).
- A search that matches only exact words, or that times out on a long query,
  or that returns everything on an empty query.
- Unstable sort between pages (sorting by a non-unique field without a
  tie-breaker), so paging repeats or skips items.
- Letting unpublished, future or draft items reach search results or deep
  links, or letting a direct link add an unpublished variant to a cart.
- Rendering merchandiser text as HTML without sanitizing it, so a product
  description can run a script on the storefront.
- Filters whose combination leads to an empty page with no way back, or
  facet counts computed on a different set than the results.

## Controversies

- **Search in the database or a search engine?** A dedicated engine (full
  text with typo tolerance, facets) gives better results, at the cost of a
  second store to keep in sync. *The choice:* start with the database's own
  full-text search, behind an interface that returns identifiers, not rows;
  move to an engine when the shop has the traffic or the catalog size to
  justify it. Why: a second store is a sync problem from day one, and most
  shops never reach the size where it pays.
- **Products with variants everywhere, or simple products too?** Many shops
  model a "simple product" without variants and sprinkle `if` around it.
  *The choice:* every product has at least one variant, always. Why: one
  shape for the cart, stock and orders is worth the one redundant row.
- **Show out-of-stock items?** Hiding them loses the sale of a restock and
  breaks shared links; showing them frustrates. *The choice:* show them,
  marked, not purchasable, offering a notify-me when accounts exist. Why:
  the link stays valid and the demand is measurable.
- **Prices in the search index?** A price depends on currency, customer
  group and promotions; indexing one price is wrong for some customer.
  *The choice:* index the base price per currency for sorting and filtering,
  and always show the price the pricing rules compute at render time. Why: a
  sort that is approximately right is fine; a displayed price that is wrong
  is not.

## Sources

- Baymard Institute, e-commerce UX research on product lists, filtering and
  search: https://baymard.com/research
- Google Search Central, Product structured data:
  https://developers.google.com/search/docs/appearance/structured-data/product
- OWASP Cross Site Scripting Prevention Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/Cross_Site_Scripting_Prevention_Cheat_Sheet.html
- Shopify, products and variants, as one widely used model:
  https://shopify.dev/docs/api/admin-graphql/latest/objects/ProductVariant
- Web Content Accessibility Guidelines (WCAG) 2.2 on images and text
  alternatives: https://www.w3.org/TR/WCAG22/
