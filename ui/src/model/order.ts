// The two orders the UI's lists are in (#147), each the same for every reader
// in every browser and locale, since people point each other at rows:
// compareText for names, keys and timestamps, compareTitle for titles a person
// wrote. String.prototype.localeCompare with no locale follows the reader's
// locale, and the lint refuses it in src.

/**
 * Orders two names, keys or timestamps by their code points: "Opus" before
 * "haiku", "aa" before "z". The order internal/model's report gives its rows
 * (#66).
 *
 * It orders every list, so it builds nothing: the first UTF-16 unit that
 * differs decides, read as the code point it is part of. Units and code points
 * order differently only where a surrogate pair meets a unit of U+E000 to
 * U+FFFF.
 */
export function compareText(a: string, b: string): number {
  const shared = Math.min(a.length, b.length)
  let at = 0
  while (at < shared && a.charCodeAt(at) === b.charCodeAt(at)) at++
  // One is the start of the other: the shorter first.
  if (at === shared) return a.length - b.length
  // The second half of a pair whose first half the two share: the pair decides.
  const inPair =
    at > 0 &&
    isHighSurrogate(a.charCodeAt(at - 1)) &&
    (isLowSurrogate(a.charCodeAt(at)) || isLowSurrogate(b.charCodeAt(at)))
  const start = inPair ? at - 1 : at
  return (a.codePointAt(start) ?? 0) - (b.codePointAt(start) ?? 0)
}

function isHighSurrogate(unit: number): boolean {
  return unit >= 0xd800 && unit <= 0xdbff
}

function isLowSurrogate(unit: number): boolean {
  return unit >= 0xdc00 && unit <= 0xdfff
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
