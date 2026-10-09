// The Brief rules demo mode refuses by, as the API server would: demo mode plays
// the API server, so it alone holds them. limits.test.ts reads
// config/crd/bases/ and fails when they differ from the CRD; the rest of the UI
// copies none of them and shows the server's message instead.

/** What the Brief CRD accepts: the idea's and each prompt's length, how many prompts, and spec.parallel. */
export const BRIEF_LIMITS = {
  idea: { min: 1, max: 4000 },
  prompt: { min: 1, max: 2000 },
  prompts: 50,
  parallel: { min: 1, max: 5 },
}

/** The API server's rule for a custom resource's name: a DNS subdomain of at most 253 characters. */
const DNS_SUBDOMAIN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

export function isBriefName(name: string): boolean {
  return name.length <= 253 && DNS_SUBDOMAIN.test(name)
}
