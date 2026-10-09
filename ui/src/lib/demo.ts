// Demo mode's sources, loaded only when the page asks for demo mode.

import { useEffect, useState } from 'react'
import type { Problem } from '../k8s/sources'

export type DemoModule = typeof import('../demo/demoSource')

/** How long demo mode waits before it tries to load its chunk again. */
const DEMO_RETRY_MS = 5000

/**
 * Demo mode's sources, loaded only in demo mode: the fixture is a chunk of its
 * own, so a page on a cluster never downloads it. The module is undefined
 * until it arrives; a chunk that fails to load is a problem, tried again.
 */
export function useDemo(demo: boolean): { module?: DemoModule; problem?: Problem } {
  const [module, setModule] = useState<DemoModule>()
  const [problem, setProblem] = useState<Problem>()
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (!demo || module) return
    let retry: ReturnType<typeof setTimeout> | undefined
    import('../demo/demoSource').then(
      (m) => {
        setProblem(undefined)
        setModule(m)
      },
      (err: unknown) => {
        setProblem({ what: 'the demo data', message: err instanceof Error ? err.message : String(err) })
        retry = setTimeout(() => setAttempt((n) => n + 1), DEMO_RETRY_MS)
      },
    )
    return () => clearTimeout(retry)
  }, [demo, module, attempt])
  return demo ? { module, problem } : {}
}
