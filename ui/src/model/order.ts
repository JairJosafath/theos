// The two orders the UI's lists are in (#147), each the same for every reader
// in every browser and locale, since people point each other at rows:
// compareText for names, keys and timestamps, compareTitle for titles a person
// wrote. String.prototype.localeCompare with no locale follows the reader's
// locale, and the lint refuses it in src.

/**
 * Orders two names, keys or timestamps by their code points: "Opus" before
 * "haiku", "aa" before "z". The order internal/model's report gives its rows
 * (#66).
 */
export function compareText(a: string, b: string): number {
  const x = Array.from(a, (c) => c.codePointAt(0) ?? 0)
  const y = Array.from(b, (c) => c.codePointAt(0) ?? 0)
  for (let i = 0; i < x.length && i < y.length; i++) {
    if (x[i] !== y[i]) return x[i] - y[i]
  }
  return x.length - y.length
}

/** Made once, with a fixed locale: the reader's browser doesn't decide. */
const TITLES = new Intl.Collator('en')

/**
 * Orders two titles a person wrote (a displayName, a label) as English does:
 * "apple" before "Zebra", capitals and small letters together. Titles the
 * collator holds equal fall back to compareText, so the order is total.
 */
export function compareTitle(a: string, b: string): number {
  return TITLES.compare(a, b) || compareText(a, b)
}
