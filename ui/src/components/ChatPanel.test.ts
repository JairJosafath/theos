import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Problem } from '../k8s/sources'
import type { Chat } from '../k8s/types'
import { ChatPanel } from './ChatPanel'

const render = (chat?: Chat, problems?: Problem[]) => renderToStaticMarkup(createElement(ChatPanel, { chat, problems }))

const webshop: Chat = {
  metadata: { name: 'webshop', namespace: 'webshop' },
  spec: { credentialsSecretRef: { name: 'matrix-bot' }, rooms: { general: 'lobby' } },
  status: {
    homeserver: 'http://theos-matrix.theos-system.svc:8008',
    space: { id: '!s:theos.internal', alias: '#theos-webshop:theos.internal' },
    rooms: [
      { purpose: 'updates', id: '!u:theos.internal', alias: '#theos-webshop-updates:theos.internal' },
      { purpose: 'general', id: '!g:theos.internal', alias: '#theos-webshop-general:theos.internal' },
    ],
    conditions: [{ type: 'Ready', status: 'True', reason: 'Ready', message: 'The Space and its 6 rooms are there' }],
  },
}

describe('the Chat panel', () => {
  it('shows nothing when the namespace has no Chat, or the cluster no Chat kind', () => {
    expect(render()).toBe('')
    expect(
      render(undefined, [{ what: 'chats', message: 'the server could not find the requested resource', code: 404 }]),
    ).toBe('')
  })

  it('says a refused read rather than hide it', () => {
    const html = render(undefined, [{ what: 'chats', message: 'chats.theos.factory.io is forbidden', code: 403 }])
    expect(html).toContain('Chat')
    expect(html).toContain('chats.theos.factory.io is forbidden')
  })

  it('shows the homeserver, Ready with its message and each room with its matrix.to link', () => {
    const html = render(webshop)
    expect(html).toContain('http://theos-matrix.theos-system.svc:8008')
    expect(html).toContain('Yes: The Space and its 6 rooms are there')
    expect(html).toContain('href="https://matrix.to/#/%23theos-webshop%3Atheos.internal"')
    expect(html).toContain('href="https://matrix.to/#/%23theos-webshop-general%3Atheos.internal"')
    expect(html).toMatch(/>lobby <svg[^]*?<\/a><span class="muted small"> general<\/span>/)
    // Rooms the operator hasn't made yet have no link.
    expect(html).toContain('<span>alerts</span><span class="muted small"> not made yet</span>')
    expect(html.match(/href="https:\/\/matrix\.to/g)).toHaveLength(3)
  })

  it('says why it is not ready', () => {
    const html = render({
      ...webshop,
      spec: { credentialsSecretRef: { name: 'matrix-bot' } },
      status: {
        conditions: [
          { type: 'Ready', status: 'False', reason: 'SecretMissing', message: 'Secret matrix-bot is missing' },
        ],
      },
    })
    expect(html).toContain('The operator&#x27;s default')
    expect(html).toContain('No (SecretMissing): Secret matrix-bot is missing')
    expect(html).toContain('Not made yet')
  })
})
