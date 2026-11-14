import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Problem } from '../k8s/sources'
import type { Chat } from '../k8s/types'
import { demoFactory } from '../demo/factoryFixture'
import { chatOf } from '../model/chat'
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

  it('#253: says each refused invite with the host code and rooms, and that the Chat keeps working', () => {
    const html = render({
      ...webshop,
      status: {
        ...webshop.status,
        refused: [
          { id: '@ghost:theos.internal', code: 'M_NOT_FOUND', rooms: ['!s:theos.internal', '!g:theos.internal'] },
          { id: '@nocode:theos.internal', rooms: ['!unknown:theos.internal'] },
        ],
        conditions: [
          { type: 'Ready', status: 'True', reason: 'Ready', message: 'The Space and its 6 rooms are there' },
          { type: 'Invited', status: 'False', reason: 'InviteRefused', message: '@ghost:theos.internal: M_NOT_FOUND' },
        ],
      },
    })
    expect(html).toContain(
      'The host refused the invite for @ghost:theos.internal: M_NOT_FOUND (rooms: the Space, lobby).',
    )
    expect(html).toContain('The host refused the invite for @nocode:theos.internal (rooms: !unknown:theos.internal).')
    expect(html).toContain('The Chat keeps working for everyone else.')
    expect(html).toContain(
      'The operator asks the host again about those rooms; check the ID in the Chat&#x27;s people.',
    )
    expect(html).toContain('<p class="chat-refused-title">Refused invites</p>')
    // Not a failure: Ready stays Yes and nothing is styled as an error.
    expect(html).toContain('Yes: The Space and its 6 rooms are there')
    expect(html).not.toContain('chat-ready-not-ready')
  })

  it('#253: shows no refusal when Invited is True, or when the last pass did not reach the invites', () => {
    const refused = [{ id: '@ghost:theos.internal', code: 'M_NOT_FOUND', rooms: ['!g:theos.internal'] }]
    const ready = {
      type: 'Ready',
      status: 'True' as const,
      reason: 'Ready',
      message: 'The Space and its 6 rooms are there',
    }
    const withInvited = (status: 'True' | 'Unknown', reason: string, list = refused) =>
      render({
        ...webshop,
        status: {
          ...webshop.status,
          refused: list,
          conditions: [ready, { type: 'Invited', status, reason, message: '' }],
        },
      })
    for (const html of [withInvited('True', 'Invited', []), withInvited('Unknown', 'NotChecked'), render(webshop)]) {
      expect(html).not.toContain('refused the invite')
      expect(html).not.toContain('Refused invites')
      expect(html).not.toContain('keeps working for everyone else')
    }
  })

  it('#162: a Matrix Chat, with or without host: matrix, renders as it did before the Slack host', () => {
    const refused: Chat = {
      ...webshop,
      status: {
        ...webshop.status,
        refused: [
          { id: '@ghost:theos.internal', code: 'M_NOT_FOUND', rooms: ['!s:theos.internal', '!g:theos.internal'] },
        ],
        conditions: [
          { type: 'Ready', status: 'True', reason: 'Ready', message: 'The Space and its 6 rooms are there' },
          { type: 'Invited', status: 'False', reason: 'InviteRefused', message: 'x' },
        ],
      },
    }
    // The markup of afd3331's panel for this Chat, its link icons shortened.
    const before = [
      '<section class="chat-panel" aria-label="Chat">',
      '<h2 class="panel-title small-title">Chat</h2>',
      '<p class="muted small">Follow and steer this project from Matrix. The rooms are invite only: ask to be added to the Chat&#x27;s people.</p>',
      '<dl class="facts chat-facts">',
      '<dt>Homeserver</dt>',
      '<dd>http://theos-matrix.theos-system.svc:8008</dd>',
      '<dt>Space</dt>',
      '<dd>',
      '<a href="https://matrix.to/#/%23theos-webshop%3Atheos.internal" target="_blank" rel="noreferrer">#theos-webshop:theos.internal <svg/>',
      '</a>',
      '</dd>',
      '<dt>Ready</dt>',
      '<dd class="chat-ready chat-ready-ready">Yes: The Space and its 6 rooms are there</dd>',
      '</dl>',
      '<div class="chat-refused" role="note" aria-label="Refused invites">',
      '<p class="chat-refused-title">Refused invites</p>',
      '<ul>',
      '<li>The host refused the invite for @ghost:theos.internal: M_NOT_FOUND (rooms: the Space, lobby).</li>',
      '</ul>',
      '<p class="muted small">The Chat keeps working for everyone else. The operator asks the host again about those rooms; check the ID in the Chat&#x27;s people.</p>',
      '</div>',
      '<ul class="chat-rooms" aria-label="Rooms">',
      '<li>',
      '<a href="https://matrix.to/#/%23theos-webshop-updates%3Atheos.internal" target="_blank" rel="noreferrer">updates <svg/>',
      '</a>',
      '</li>',
      '<li>',
      '<a href="https://matrix.to/#/%23theos-webshop-general%3Atheos.internal" target="_blank" rel="noreferrer">lobby <svg/>',
      '</a>',
      '<span class="muted small"> general</span>',
      '</li>',
      '<li>',
      '<span>triage</span>',
      '<span class="muted small"> not made yet</span>',
      '</li>',
      '<li>',
      '<span>specs</span>',
      '<span class="muted small"> not made yet</span>',
      '</li>',
      '<li>',
      '<span>reviews</span>',
      '<span class="muted small"> not made yet</span>',
      '</li>',
      '<li>',
      '<span>alerts</span>',
      '<span class="muted small"> not made yet</span>',
      '</li>',
      '</ul>',
      '</section>',
    ].join('')
    const plain = (chat: Chat) => render(chat).replace(/<svg.*?<\/svg>/g, '<svg/>')
    expect(plain(refused)).toBe(before)
    expect(plain({ ...refused, spec: { ...refused.spec, host: 'matrix' } })).toBe(before)
    const html = render(refused)
    expect(html).toContain('Homeserver')
    expect(html).toContain('Space')
    expect(html).not.toContain('Slack')
  })

  describe('#162: a Chat on Slack', () => {
    const slack: Chat = {
      metadata: { name: 'webshop-slack', namespace: 'webshop-slack' },
      spec: {
        host: 'slack',
        credentialsSecretRef: { name: 'slack-bot' },
        aliasPrefix: 'acme_shop',
        slack: {
          people: [
            { slackID: 'U0TESTADA01', user: 'ada@example.com', groups: ['developers', 'ops'] },
            { slackID: 'W0TESTBOB02', user: 'bob@example.com' },
          ],
        },
      },
      status: {
        homeserver: 'https://slack.com/api',
        bot: 'U0TESTBOT00',
        rooms: [
          { purpose: 'updates', id: 'C0TESTUPD01', alias: '#acme_shop-updates' },
          { purpose: 'specs', id: 'C0TESTSPE02', alias: '#acme_shop-specs' },
        ],
        conditions: [
          { type: 'Ready', status: 'True', reason: 'Ready', message: 'The 6 rooms are ready, with no Space' },
        ],
      },
    }

    it('says the host, with no Space and no homeserver', () => {
      const html = render(slack)
      expect(html).toContain('<dt>Host</dt><dd>Slack</dd>')
      expect(html).toContain('from Slack')
      expect(html).toContain('Yes: The 6 rooms are ready, with no Space')
      for (const gone of ['Space</dt>', 'Homeserver', 'slack.com/api', 'matrix.to', 'Not made yet']) {
        expect(html).not.toContain(gone)
      }
      expect(html).not.toContain('<a ')
    })

    it('shows each room as its channel: the name the operator made, else the one it will make', () => {
      const html = render(slack)
      expect(html).toContain('<span>#acme_shop-updates</span><span class="muted small"> updates</span></li>')
      expect(html).toContain('<span>#acme_shop-specs</span><span class="muted small"> specs</span></li>')
      expect(html).toContain(
        '<span>#acme_shop-triage</span><span class="muted small"> triage</span><span class="muted small"> not made yet</span>',
      )
      // The channel's ID is not shown as a name.
      expect(html).not.toContain('C0TESTUPD01')
      expect(html.match(/ not made yet/g)).toHaveLength(4)
    })

    it('names the channels after the namespace when the Chat names no aliasPrefix', () => {
      const html = render({ ...slack, spec: { ...slack.spec, aliasPrefix: undefined }, status: undefined })
      expect(html).toContain('<span>#theos-webshop-slack-general</span>')
      expect(html).toContain('Unknown: The operator has not reported on it yet.')
    })

    it('lists the people of spec.slack.people by Slack ID', () => {
      const html = render(slack)
      expect(html).toContain('<li><span>U0TESTADA01</span></li>')
      expect(html).toContain('<li><span>W0TESTBOB02</span></li>')
      expect(render({ ...slack, spec: { ...slack.spec, slack: undefined } })).toContain('No one is listed')
    })

    it('#162: does not show the Kubernetes user or groups of a person', () => {
      const html = render(slack)
      for (const hidden of ['ada@example.com', 'bob@example.com', 'developers', 'ops)']) {
        expect(html).not.toContain(hidden)
      }
    })

    it('shows a refused invite by Slack ID, naming the channels', () => {
      const html = render({
        ...slack,
        status: {
          ...slack.status,
          refused: [{ id: 'U0TESTGHO03', code: 'user_not_found', rooms: ['C0TESTSPE02'] }],
          conditions: [
            ...slack.status!.conditions!,
            { type: 'Invited', status: 'False', reason: 'InviteRefused', message: 'x' },
          ],
        },
      })
      expect(html).toContain('The host refused the invite for U0TESTGHO03: user_not_found (rooms: #acme_shop-specs).')
      // On Slack the note points at slack.people; Matrix's wording is unchanged.
      expect(html).toContain('check the ID in the Chat&#x27;s slack.people.')
    })

    it('renders what the Chat holds as text, never as HTML', () => {
      const html = render({
        ...slack,
        spec: {
          ...slack.spec,
          slack: { people: [{ slackID: 'U0<img src=x>', user: '<script>alert(1)</script>', groups: ['<b>'] }] },
        },
      })
      expect(html).not.toContain('<img')
      expect(html).not.toContain('<script>')
      expect(html).toContain('U0&lt;img src=x&gt;')
    })

    it('is in the demo fixture beside the Matrix Chat, each in its own namespace', () => {
      const { chats } = demoFactory(Date.parse('2026-11-14T00:00:00Z'))
      const matrix = chatOf(chats, 'webshop')!
      const onSlack = chatOf(chats, 'webshop-slack')!
      expect(matrix.spec.host).toBeUndefined()
      expect(onSlack.spec.host).toBe('slack')
      expect(render(matrix)).toContain('Homeserver')
      const html = render(onSlack)
      expect(html).toContain('<dt>Host</dt><dd>Slack</dd>')
      expect(html).toContain('#theos-webshop-slack-alerts')
      expect(html).toContain('U0DEMOADA01')
      expect(html).not.toContain('Homeserver')
      // One channel not made yet and one refused person, so the demo shows them.
      expect(html).toContain('not made yet')
      expect(html).toContain('The host refused the invite for U0DEMOGHO03')
    })
  })
})
