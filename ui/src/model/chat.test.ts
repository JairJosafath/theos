import { describe, expect, it } from 'vitest'
import type { Chat } from '../k8s/types'
import { chatHost, chatOf, chatSummary, matrixToHref } from './chat'

function chat(name: string, more: Partial<Chat> = {}, created = '2026-11-05T10:00:00Z'): Chat {
  return {
    metadata: { name, namespace: 'webshop', creationTimestamp: created },
    spec: { credentialsSecretRef: { name: 'matrix-bot' } },
    ...more,
  }
}

const ready = (status: 'True' | 'False', reason: string, message = '') => ({
  type: 'Ready',
  status,
  reason,
  message,
})

describe('chatOf', () => {
  it('finds nothing without a namespace or a Chat in it', () => {
    expect(chatOf([chat('webshop')], undefined)).toBeUndefined()
    expect(chatOf([chat('webshop')], 'shop')).toBeUndefined()
  })

  it('takes the Ready one of two, else the oldest', () => {
    const first = chat('first', {}, '2026-11-05T09:00:00Z')
    const second = chat('second', { status: { conditions: [ready('True', 'Ready')] } }, '2026-11-05T11:00:00Z')
    expect(chatOf([second, first], 'webshop')).toBe(second)
    const dup = chat('dup', { status: { conditions: [ready('False', 'Duplicate')] } }, '2026-11-05T11:00:00Z')
    expect(chatOf([dup, first], 'webshop')).toBe(first)
  })
})

describe('matrixToHref', () => {
  it('percent-encodes an alias or a room ID', () => {
    expect(matrixToHref('#theos-webshop-general:theos.internal')).toBe(
      'https://matrix.to/#/%23theos-webshop-general%3Atheos.internal',
    )
    expect(matrixToHref('!abc:theos.internal')).toBe('https://matrix.to/#/!abc%3Atheos.internal')
  })
})

describe('chatSummary', () => {
  it('lists the six rooms by their names, each linked by alias, else ID, once made', () => {
    const s = chatSummary(
      chat('webshop', {
        spec: {
          credentialsSecretRef: { name: 'matrix-bot' },
          homeserver: 'https://matrix.example.com',
          rooms: { general: 'lobby' },
        },
        status: {
          homeserver: 'https://matrix.example.com',
          space: { id: '!space:example.com', alias: '#theos-webshop:example.com' },
          rooms: [
            { purpose: 'general', id: '!g:example.com', alias: '#theos-webshop-general:example.com' },
            { purpose: 'alerts', id: '!a:example.com' },
          ],
          conditions: [ready('True', 'Ready', 'The Space and its 6 rooms are there')],
        },
      }),
    )
    expect(s.homeserver).toBe('https://matrix.example.com')
    expect(s.space).toEqual({
      target: '#theos-webshop:example.com',
      href: 'https://matrix.to/#/%23theos-webshop%3Aexample.com',
    })
    expect(s.rooms.map((r) => [r.purpose, r.name, r.target])).toEqual([
      ['updates', 'updates', undefined],
      ['general', 'lobby', '#theos-webshop-general:example.com'],
      ['triage', 'triage', undefined],
      ['specs', 'specs', undefined],
      ['reviews', 'reviews', undefined],
      ['alerts', 'alerts', '!a:example.com'],
    ])
    expect(s.rooms[5].href).toBe('https://matrix.to/#/!a%3Aexample.com')
    expect(s.ready).toEqual({ state: 'ready', reason: 'Ready', message: 'The Space and its 6 rooms are there' })
  })

  it("says Ready=False with the condition's words, and nothing reported before the operator has", () => {
    const refused = chatSummary(
      chat('webshop', {
        status: {
          conditions: [ready('False', 'CredentialRefused', 'The homeserver refused the bot token: M_UNKNOWN_TOKEN')],
        },
      }),
    )
    expect(refused.ready).toEqual({
      state: 'not-ready',
      reason: 'CredentialRefused',
      message: 'The homeserver refused the bot token: M_UNKNOWN_TOKEN',
    })
    expect(refused.homeserver).toBeUndefined()
    expect(refused.space).toBeUndefined()
    expect(chatSummary(chat('webshop')).ready.state).toBe('unknown')
  })

  it('#251/#253: lists refusals only while Invited is False, naming rooms as the Chat does', () => {
    const base = {
      spec: { credentialsSecretRef: { name: 'matrix-bot' }, rooms: { general: 'lobby' } },
      status: {
        space: { id: '!s:example.com' },
        rooms: [{ purpose: 'general' as const, id: '!g:example.com' }],
        refused: [
          {
            id: '@ghost:example.com',
            code: 'M_NOT_FOUND',
            rooms: ['!s:example.com', '!g:example.com', '!x:example.com'],
          },
        ],
        conditions: [{ type: 'Invited', status: 'False' as const, reason: 'InviteRefused', message: '' }],
      },
    }
    expect(chatSummary(chat('webshop', base)).refused).toEqual([
      { id: '@ghost:example.com', code: 'M_NOT_FOUND', rooms: ['the Space', 'lobby', '!x:example.com'] },
    ])
    const stale = (status: 'True' | 'Unknown') =>
      chatSummary(
        chat('webshop', {
          ...base,
          status: { ...base.status, conditions: [{ type: 'Invited', status, reason: 'x', message: '' }] },
        }),
      )
    expect(stale('True').refused).toEqual([])
    expect(stale('Unknown').refused).toEqual([])
    expect(chatSummary(chat('webshop')).refused).toEqual([])
  })

  it('#162: a Chat with no host, or host: matrix, is on Matrix and keeps its Space and homeserver', () => {
    const base = chat('webshop', {
      spec: { credentialsSecretRef: { name: 'matrix-bot' }, homeserver: 'https://matrix.example.com' },
      status: { space: { id: '!s:example.com', alias: '#theos-webshop:example.com' } },
    })
    for (const c of [base, { ...base, spec: { ...base.spec, host: 'matrix' as const } }]) {
      const s = chatSummary(c)
      expect(chatHost(c)).toBe('matrix')
      expect(s.host).toBe('matrix')
      expect(s.homeserver).toBe('https://matrix.example.com')
      expect(s.space?.target).toBe('#theos-webshop:example.com')
      expect(s.people).toEqual([])
    }
  })

  it('#162: a Slack Chat has no Space, no homeserver and no link, its rooms are channels and its people are by slackID', () => {
    const s = chatSummary(
      chat('webshop', {
        spec: {
          host: 'slack',
          credentialsSecretRef: { name: 'slack-bot' },
          // A Slack Chat names no homeserver and no space (CEL); status holds what the operator reached.
          slack: {
            people: [
              { slackID: 'U0TESTADA01', user: 'ada@example.com', groups: ['developers'] },
              { slackID: 'U0TESTBOB02', user: 'bob@example.com' },
            ],
          },
        },
        status: {
          homeserver: 'https://slack.com/api',
          space: { id: '!ignored:example.com' },
          rooms: [
            { purpose: 'general', id: 'C0TESTGEN01', alias: '#theos-webshop-general' },
            { purpose: 'alerts', id: 'C0TESTALE02' },
          ],
          conditions: [ready('True', 'Ready', '')],
        },
      }),
    )
    expect(s.host).toBe('slack')
    expect(s.homeserver).toBeUndefined()
    expect(s.space).toBeUndefined()
    expect(s.rooms.map((r) => [r.purpose, r.name, r.target, r.href])).toEqual([
      ['updates', '#theos-webshop-updates', undefined, undefined],
      ['general', '#theos-webshop-general', '#theos-webshop-general', undefined],
      ['triage', '#theos-webshop-triage', undefined, undefined],
      ['specs', '#theos-webshop-specs', undefined, undefined],
      ['reviews', '#theos-webshop-reviews', undefined, undefined],
      // Made, but the operator stored no alias: the ID is all the Chat carries, and is shown as it is.
      ['alerts', 'C0TESTALE02', 'C0TESTALE02', undefined],
    ])
    expect(s.people).toEqual([
      { id: 'U0TESTADA01', user: 'ada@example.com', groups: ['developers'] },
      { id: 'U0TESTBOB02', user: 'bob@example.com', groups: [] },
    ])
    expect(s.ready).toEqual({ state: 'ready', reason: 'Ready', message: 'The channels are there.' })
  })

  it('#162: a Slack Chat names its channels after its aliasPrefix, and lists no people it holds none of', () => {
    const s = chatSummary(
      chat('webshop', {
        spec: { host: 'slack', credentialsSecretRef: { name: 'slack-bot' }, aliasPrefix: 'acme_shop' },
      }),
    )
    expect(s.rooms[0].name).toBe('#acme_shop-updates')
    expect(s.people).toEqual([])
  })
})
