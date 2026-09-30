// Tests for scripts/ansi.mjs, the ANSI-to-HTML converter behind term-shot.mjs.
// Run with: node --test test/ansi.test.mjs

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { ansiToHtmlLines, applySgr, PALETTE, resolveCarriageReturns, xterm256 } from '../scripts/ansi.mjs'

describe('ansiToHtmlLines', () => {
  it('keeps plain text and escapes HTML', () => {
    assert.deepEqual(ansiToHtmlLines('a < b & c\nsecond\n'), ['a &lt; b &amp; c', 'second'])
  })

  it('colours and bolds text, and resets', () => {
    const [line] = ansiToHtmlLines('\x1b[1;32mok\x1b[0m done')
    assert.equal(line, `<span style="color:${PALETTE[2]};font-weight:700">ok</span> done`)
  })

  it('carries a style across lines until reset, like a terminal', () => {
    const lines = ansiToHtmlLines('\x1b[31mred\nstill red\x1b[39m\nplain')
    assert.match(lines[1], /color:#ff7b72/)
    assert.equal(lines[2], 'plain')
  })

  it('understands 256 and 24-bit colours, foreground and background', () => {
    const [line] = ansiToHtmlLines('\x1b[38;5;196;48;2;1;2;3mx')
    assert.equal(line, '<span style="color:#ff0000;background:#010203">x</span>')
  })

  it('drops other escape sequences and hyperlinks, keeping their text', () => {
    const [line] = ansiToHtmlLines('\x1b[2K\x1b[1Gstart \x1b]8;;http://x\x07link\x1b]8;;\x07 end\x1b(B')
    assert.equal(line, 'start link end')
  })

  it('shows only the last state of a progress line, and handles CRLF', () => {
    assert.deepEqual(ansiToHtmlLines('10%\r50%\r100%\r\nnext\r\n'), ['100%', 'next'])
    assert.equal(resolveCarriageReturns('abc\r'), 'abc')
  })

  it('expands tabs to 8-column stops', () => {
    assert.deepEqual(ansiToHtmlLines('ab\tc'), ['ab      c'])
  })
})

describe('applySgr', () => {
  it('handles inverse, dim and bright colours', () => {
    const s = applySgr({}, '2;7;94')
    assert.equal(s.dim, true)
    assert.equal(s.inverse, true)
    assert.equal(s.fg, PALETTE[12])
    assert.equal(applySgr(s, '').inverse, false)
  })

  it('maps the xterm colour cube and grey ramp', () => {
    assert.equal(xterm256(16), '#000000')
    assert.equal(xterm256(231), '#ffffff')
    assert.equal(xterm256(232), '#080808')
  })
})
