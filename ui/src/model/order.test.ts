import { afterEach, describe, expect, it, vi } from 'vitest'
import { compareText, compareTitle } from './order'

/** first goes before second, whichever way round the sort meets them. */
function expectOrder(compare: (a: string, b: string) => number, first: string, second: string): void {
  expect(compare(first, second)).toBeLessThan(0)
  expect(compare(second, first)).toBeGreaterThan(0)
  expect([second, first].sort(compare)).toEqual([first, second])
}

/** Whether this Node's ICU knows the locale; a small-ICU build falls back to its own. */
function knows(locale: string): boolean {
  return Intl.Collator.supportedLocalesOf([locale]).length > 0
}

// #147: the UI's lists were ordered by localeCompare with no locale, the
// reader's locale's order, so two readers saw the same list in different
// orders. These are the names locales disagree on.
const DISAGREED = [
  // Danish holds "aa" for a letter of its own, after "z".
  ['aa', 'z', 'da'],
  // Czech holds "ch" for one, after "h".
  ['ch', 'h', 'cs'],
] as const

// Skipped, and shown as skipped, on a Node whose ICU lacks the locale.
describe('the names of #147, which locales disagree on', () => {
  for (const [first, second, locale] of DISAGREED) {
    it.skipIf(!knows(locale))(`${first} and ${second} are the other way round in ${locale}`, () => {
      expect(new Intl.Collator(locale).compare(first, second)).toBeGreaterThan(0)
    })
  }
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
    // #147: so are the last units of the Basic Plane, U+E000 and U+FFFD, above every surrogate.
    ['\ue000', '\u{1f600}'],
    ['\ufffd', '\u{1f600}'],
    ['a\ufffd', 'a\u{10000}'],
    // #147: two pairs that share their first unit, and a pair against that unit alone:
    // U+1F600 is 0xD83D 0xDE00, and 0xD83D alone is before it whatever follows.
    ['\u{1f600}', '\u{1f601}'],
    ['\ud83d\ue000', '\u{1f600}'],
    ['\ud83d\ue000', '\ud83d\ude00a'],
    // #147: one the start of the other, a pair's first unit alone included.
    ['orders', 'orders2'],
    ['a\u{1f600}', 'a\u{1f600}\u{1f600}'],
    ['a\ud83d', 'a\u{1f600}'],
    ['', 'a'],
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

  // #147: on an English machine a collator made with no locale orders as
  // 'en' does, so no case above tells the two apart. Here the machine's locale
  // is another: Intl.Collator with no locale gives that locale's collator, as
  // a browser set to it does, and the module is loaded afresh.
  describe('on a machine of another locale', () => {
    const real = Intl.Collator
    afterEach(() => {
      Intl.Collator = real
      vi.resetModules()
    })

    for (const [first, second, locale] of DISAGREED) {
      it.skipIf(!knows(locale))(`puts ${first} before ${second} where the machine's locale is ${locale}`, async () => {
        Intl.Collator = function (locales?: Intl.LocalesArgument, options?: Intl.CollatorOptions) {
          return new real(locales ?? locale, options)
        } as typeof Intl.Collator
        // eslint-disable-next-line no-restricted-syntax -- the machine's own order is what this case sets up
        expect(new Intl.Collator().compare(first, second)).toBeGreaterThan(0)
        vi.resetModules()
        const fresh = await import('./order')
        expectOrder(fresh.compareTitle, first, second)
      })
    }
  })

  it('orders titles the collator holds equal by their code points', () => {
    // "é" as one code point and as "e" with a combining accent.
    expect(new Intl.Collator('en').compare('\u00e9', 'e\u0301')).toBe(0)
    expectOrder(compareTitle, 'e\u0301', '\u00e9')
  })
})
