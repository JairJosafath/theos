/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { createApiHandler, resolveUpstream } from './server.mjs'

// Where `npm run dev` reads the cluster: a `kubectl proxy` by default, the same
// upstream server.mjs uses outside a cluster.
const k8sApi = process.env.K8S_API ?? 'http://127.0.0.1:8001'

// The dev server answers /k8s and /k8s-watch with server.mjs's own handler, so
// development is read-only and multiplexes watches exactly like production.
function k8sGateway(): Plugin {
  return {
    name: 'theos-k8s-gateway',
    configureServer(server) {
      const api = createApiHandler({
        upstream: resolveUpstream({ K8S_API: k8sApi }),
        log: (msg: string) => server.config.logger.warn(msg),
      })
      server.middlewares.use((req, res, next) => {
        if (!api(req, res)) next()
      })
    },
  }
}

export default defineConfig({
  // Relative asset URLs, so the build works behind any path prefix.
  base: './',
  // The release this UI is: the image's VERSION build arg (Dockerfile), dev otherwise.
  define: {
    __C4_UI_VERSION__: JSON.stringify(process.env.THEOS_UI_VERSION || 'dev'),
  },
  plugins: [react(), k8sGateway()],
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
    // Three workers, not Vitest's one per core but one: make check runs these
    // tests in one of the device's heavy-job slots, beside Go builds and envtest,
    // and eleven workers each spawning a process per file stalled tests that
    // take milliseconds past the 5 s timeout (#1193). Files stay isolated: some
    // stub `window` for the whole file, and Account.tsx reads it.
    maxWorkers: 3,
  },
})
