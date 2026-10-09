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

/** Whether the demo takes name for a Brief: a DNS label of at most 63 characters. */
const DNS_LABEL = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

export function isBriefName(name: string): boolean {
  return name.length <= 63 && DNS_LABEL.test(name)
}
