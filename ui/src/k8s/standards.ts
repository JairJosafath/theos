// The Standard page's write, with actions on (design.md, UI actions): set its
// pinned spec.template.ref, which makes the operator open an update pull
// request on each Ready Container that uses it. server.mjs passes on exactly
// this patch; that a ref is a tag or commit is the operator's rule
// (UnpinnedRef), so the ref goes as typed and a refused write shows the API
// server's message.

import { resourcePath, RESOURCES } from './types'
import { errorFromResponse } from './watch'

/** The JSON patch that pins a Standard's template at `ref`. */
export function refPatch(ref: string): { op: 'add'; path: '/spec/template/ref'; value: string }[] {
  return [{ op: 'add', path: '/spec/template/ref', value: ref }]
}

/** Bumps a Standard's ref; rejects with the API's message when it refuses. */
export interface StandardWriter {
  bumpRef(namespace: string, name: string, ref: string): Promise<void>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveStandardWriter implements StandardWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch

  constructor(baseUrl: string, fetchImpl: typeof fetch = (input, init) => fetch(input, init)) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
  }

  async bumpRef(namespace: string, name: string, ref: string): Promise<void> {
    const path = `${resourcePath(RESOURCES.standards, namespace)}/${encodeURIComponent(name)}`
    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify(refPatch(ref)),
    })
    if (!res.ok) throw await errorFromResponse(res)
  }
}
