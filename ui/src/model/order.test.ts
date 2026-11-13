import { describe, expect, it } from 'vitest'
import { compareText, compareTitle } from './order'

/** first goes before second, whichever way round the sort meets them. */
function expectOrder(compare: (a: string, b: string) => number, first: string, second: string): void {
  expect(compare(first, second)).toBeLessThan(0)
  expect(compare(second, first)).toBeGreaterThan(0)
  expect([second, first].sort(compare)).toEqual([first, second])
}

// #147: the UI's lists were ordered by localeCompare with no locale, the
// reader's locale's order, so two readers saw the same list in different
// orders. These are the names locales disagree on.
describe('the names of #147, which locales disagree on', () => {
  it.each([
    // Danish holds "aa" for a letter of its own, after "z".
    ['aa', 'z', 'da'],
    // Czech holds "ch" for one, after "h".
    ['ch', 'h', 'cs'],
  ])('%s and %s are the other way round in %s', (first, second, locale) => {
    expect(new Intl.Collator(locale).compare(first, second)).toBeGreaterThan(0)
  })
})

// #66: rows with as many Tasks were ordered by localeCompare, the reader's
// locale's order, where internal/model's report orders by code points.
// #147: so is every list of names, keys and timestamps.
describe('compareText', () => {
  it.each([
    ['claude-code · Opus', 'claude-code · haiku'],
    ['claude-code · m-1', 'claude-code · m_1'],
    ['Z', 'a'],
    ['z', 'ä'],
    ['a', 'a b'],
    // A code point, not a UTF-16 unit: U+FF5A is before U+1F600, whose first unit is 0xD83D.
    ['\uff5a', '\u{1f600}'],
    // #147: the names locales disagree on.
    ['aa', 'z'],
    ['ch', 'h'],
    // #147: a name with a hyphen and one without.
    ['orders-api', 'ordersapi'],
    ['orders', 'orders-api'],
    ['orders-api-21', 'orders-api-3'],
    // #147: a key joined from parts, and a timestamp as the API writes it.
    ['shop/orders-api', 'shop/ordersapi'],
    ['2026-11-05T09:59:59Z', '2026-11-05T10:00:00Z'],
  ])('puts %s before %s', (first, second) => {
    expectOrder(compareText, first, second)
  })

  it('finds a name equal to itself', () => {
    expect(compareText('claude-code · opus', 'claude-code · opus')).toBe(0)
  })
})

// #147: titles a person wrote go through one collator with a fixed locale.
describe('compareTitle', () => {
  it.each([
    // The names locales disagree on.
    ['aa', 'z'],
    ['ch', 'h'],
    // A name with a hyphen and one without.
    ['orders-api', 'ordersapi'],
    ['orders', 'orders-api'],
    // Capitals and small letters together, where code points put "Zebra" first.
    ['apple', 'Zebra'],
    ['Apple', 'zebra'],
    ['Orders API', 'payments'],
    // The same letters: the small one first.
    ['apple', 'Apple'],
  ])('puts %s before %s', (first, second) => {
    expectOrder(compareTitle, first, second)
  })

  it('finds a title equal to itself', () => {
    expect(compareTitle('Orders API', 'Orders API')).toBe(0)
  })

  it('orders titles the collator holds equal by their code points', () => {
    // "é" as one code point and as "e" with a combining accent.
    expect(new Intl.Collator('en').compare('\u00e9', 'e\u0301')).toBe(0)
    expectOrder(compareTitle, 'e\u0301', '\u00e9')
  })
})
