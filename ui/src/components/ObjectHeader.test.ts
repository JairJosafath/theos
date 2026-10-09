import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Container } from '../k8s/types'
import { containerNextStep } from '../model/lifecycle'
import { ObjectHeader, type ObjectHeaderProps } from './ObjectHeader'

const failed: Container = {
  metadata: { name: 'catalog-api', namespace: 'webshop' },
  spec: {
    softwareSystemRef: { name: 'webshop-core' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'python-fastapi' },
  },
  status: {
    phase: 'Failed',
    repositoryURL: 'https://gitea.example/webshop/catalog-api',
    conditions: [
      { type: 'ReferencesResolved', status: 'True', reason: 'Resolved' },
      { type: 'ChoicesValid', status: 'True', reason: 'Valid' },
      { type: 'RepositoryCreated', status: 'True', reason: 'Created' },
      {
        type: 'Scaffolded',
        status: 'False',
        reason: 'ForeignCommits',
        message: 'the repository has commits the factory did not make',
      },
    ],
  },
}

function render(c: Container, extra: Partial<ObjectHeaderProps> = {}) {
  return renderToStaticMarkup(
    createElement(ObjectHeader, {
      kind: 'Container',
      name: c.metadata.name,
      namespace: c.metadata.namespace!,
      breadcrumb: [{ label: 'Project webshop', href: '?area=story' }],
      phase: c.status?.phase,
      conditions: c.status?.conditions ?? [],
      childrenTitle: 'Relationships',
      children: [],
      links: [],
      next: containerNextStep(c),
      ...extra,
    }),
  )
}

/** The text of the page, as a reader sees it: tags dropped, entities decoded. */
function text(html: string) {
  return html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;|&#39;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
}

describe('ObjectHeader for a Failed Container', () => {
  const html = render(failed)

  it('marks Failed as the current step, off the path', () => {
    expect(html).toMatch(
      /<ol class="stepper-exits"[^>]*><li class="step step-current step-tone-error"[^>]*aria-current="step"/,
    )
    const path = /<ol class="stepper-path"[^]*?<\/ol>/.exec(html)![0]
    expect(path).not.toContain('step-current')
    expect(path.match(/step-off/g)).toHaveLength(3)
  })

  it('shows the failed condition with its reason and message', () => {
    const t = text(html)
    expect(t).toContain('Scaffolded: no · ForeignCommits')
    expect(t).toContain('the repository has commits the factory did not make')
  })

  it('says to empty the repository, links it, and gives a kubectl retry line that works as printed', () => {
    const t = text(html)
    expect(t).toContain('Empty it, then retry the scaffold.')
    expect(html).toContain('href="https://gitea.example/webshop/catalog-api"')
    const command = /<code[^>]*>([^<]*)<\/code>/.exec(html)![1].replace(/&#x27;/g, "'")
    expect(command).toBe(
      'kubectl -n webshop annotate containers.c4.factory.io catalog-api c4.factory.io/retry=$(date +%s) --overwrite',
    )
    // It names this Container, in its namespace, and the annotation the operator watches.
    const args = command.split(' ')
    expect(args.slice(0, 5)).toEqual(['kubectl', '-n', 'webshop', 'annotate', 'containers.c4.factory.io'])
    expect(args[5]).toBe(failed.metadata.name)
    expect(args[6]).toMatch(/^c4\.factory\.io\/retry=/)
    expect(html).toContain('aria-label="Copy the command"')
  })

  it('names the kind and the breadcrumb back to its parent', () => {
    expect(html).toContain('<a href="?area=story">Project webshop</a>')
    expect(html).toContain('<li aria-current="page">Container catalog-api</li>')
  })
})

describe('ObjectHeader for a Ready Container', () => {
  it('says what a person does from the lifecycle table, and gives no command', () => {
    const ready: Container = {
      ...failed,
      status: { phase: 'Ready', conditions: [{ type: 'Scaffolded', status: 'True', reason: 'Scaffolded' }] },
    }
    const html = render(ready)
    expect(text(html)).toContain('Work in its repository.')
    expect(html).not.toContain('<code>')
    expect(html).toMatch(/step step-done[^>]*>.*Pending/)
  })
})
