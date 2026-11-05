import { describe, expect, it } from 'vitest'
import type { Chat } from '../k8s/types'
import { chatOf, chatSummary, matrixToHref } from './chat'

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
})
