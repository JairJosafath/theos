// Turns terminal output with ANSI escape codes into HTML: SGR colours (16, 256
// and 24-bit), bold, dim, italic, underline, inverse and strikethrough. Other
// escape sequences (cursor moves, window titles, hyperlinks) are dropped;
// carriage-return progress lines keep only their last state.

/** Colours of the 16 basic ANSI colours, tuned for a dark background. */
export const PALETTE = [
  '#5c6370',
  '#ff7b72',
  '#7ee787',
  '#e3b341',
  '#79c0ff',
  '#d2a8ff',
  '#56d4dd',
  '#c9d1d9',
  '#8b949e',
  '#ffa198',
  '#aff5b4',
  '#f8e3a1',
  '#a5d6ff',
  '#e2c5ff',
  '#b3f0ff',
  '#ffffff',
]

export const DEFAULT_FG = '#d1d7e0'
export const DEFAULT_BG = '#0d1117'

/** Colour n (0-255) of the xterm 256-colour palette. */
export function xterm256(n) {
  if (n < 16) return PALETTE[n]
  if (n < 232) {
    const levels = [0, 95, 135, 175, 215, 255]
    const i = n - 16
    return rgb(levels[Math.floor(i / 36)], levels[Math.floor(i / 6) % 6], levels[i % 6])
  }
  const v = 8 + (n - 232) * 10
  return rgb(v, v, v)
}

function rgb(r, g, b) {
  return `#${[r, g, b].map((v) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, '0')).join('')}`
}

export function escapeHtml(text) {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

const RESET = {
  fg: undefined,
  bg: undefined,
  bold: false,
  dim: false,
  italic: false,
  underline: false,
  inverse: false,
  strike: false,
}

/** Applies one SGR sequence's parameters to a style, returning the new style. */
export function applySgr(style, params) {
  const s = { ...style }
  const codes = params === '' ? [0] : params.split(/[;:]/).map((p) => (p === '' ? 0 : Number(p)))
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i]
    if (c === 0) Object.assign(s, RESET)
    else if (c === 1) s.bold = true
    else if (c === 2) s.dim = true
    else if (c === 3) s.italic = true
    else if (c === 4) s.underline = true
    else if (c === 7) s.inverse = true
    else if (c === 9) s.strike = true
    else if (c === 22) s.bold = s.dim = false
    else if (c === 23) s.italic = false
    else if (c === 24) s.underline = false
    else if (c === 27) s.inverse = false
    else if (c === 29) s.strike = false
    else if (c >= 30 && c <= 37) s.fg = PALETTE[c - 30]
    else if (c >= 90 && c <= 97) s.fg = PALETTE[c - 90 + 8]
    else if (c === 39) s.fg = undefined
    else if (c >= 40 && c <= 47) s.bg = PALETTE[c - 40]
    else if (c >= 100 && c <= 107) s.bg = PALETTE[c - 100 + 8]
    else if (c === 49) s.bg = undefined
    else if (c === 38 || c === 48) {
      // 38;5;n or 38;2;r;g;b (and the same for background with 48).
      let colour
      if (codes[i + 1] === 5) {
        colour = xterm256(codes[i + 2] ?? 0)
        i += 2
      } else if (codes[i + 1] === 2) {
        colour = rgb(codes[i + 2] ?? 0, codes[i + 3] ?? 0, codes[i + 4] ?? 0)
        i += 4
      }
      if (colour) s[c === 38 ? 'fg' : 'bg'] = colour
    }
  }
  return s
}

function styleCss(s) {
  let fg = s.fg
  let bg = s.bg
  if (s.inverse) [fg, bg] = [bg ?? DEFAULT_BG, fg ?? DEFAULT_FG]
  const css = []
  if (fg) css.push(`color:${fg}`)
  if (bg) css.push(`background:${bg}`)
  if (s.bold) css.push('font-weight:700')
  if (s.dim) css.push('opacity:.65')
  if (s.italic) css.push('font-style:italic')
  const lines = [s.underline && 'underline', s.strike && 'line-through'].filter(Boolean)
  if (lines.length) css.push(`text-decoration:${lines.join(' ')}`)
  return css.join(';')
}

/** Keeps what a terminal would show after carriage returns overwrite a line. */
export function resolveCarriageReturns(line) {
  if (!line.includes('\r')) return line
  const parts = line.split('\r')
  // A trailing \r (as in \r\n) changes nothing visible.
  while (parts.length > 1 && parts.at(-1) === '') parts.pop()
  return parts.at(-1)
}

// CSI sequences (ESC [ ... final byte), OSC sequences (ESC ] ... BEL or ESC \),
// and two-byte escapes such as charset selection. Matching control characters
// is the point here.
const ESCAPES =
  // eslint-disable-next-line no-control-regex
  /\x1b\[([0-9;:?<=>]*)([ -/]*[@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHMc]/g

// eslint-disable-next-line no-control-regex
const CONTROL = /[\x00-\x08\x0b-\x1f\x7f]/g

/**
 * Converts ANSI text to HTML spans, one string per line.
 * @param {string} text
 * @returns {string[]}
 */
export function ansiToHtmlLines(text) {
  const out = []
  let style = { ...RESET }
  const normalised = text.replace(/\r\n/g, '\n').replace(/\n$/, '')
  for (const rawLine of normalised.split('\n')) {
    const line = resolveCarriageReturns(rawLine)
    let html = ''
    let column = 0
    let last = 0
    const emit = (raw) => {
      // Control characters (and any escape we did not recognise) print nothing.
      const chunk = raw.replace(CONTROL, '')
      if (!chunk) return
      // Expand tabs to the next multiple of 8 columns, as terminals do.
      let expanded = ''
      for (const ch of chunk) {
        if (ch === '\t') {
          const spaces = 8 - (column % 8)
          expanded += ' '.repeat(spaces)
          column += spaces
        } else {
          expanded += ch
          column++
        }
      }
      const css = styleCss(style)
      html += css ? `<span style="${css}">${escapeHtml(expanded)}</span>` : escapeHtml(expanded)
    }
    for (const m of line.matchAll(ESCAPES)) {
      emit(line.slice(last, m.index))
      last = m.index + m[0].length
      if (m[2] === 'm') style = applySgr(style, m[1])
    }
    emit(line.slice(last))
    out.push(html)
  }
  return out
}
