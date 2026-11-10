import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { MOCK_APP_ORIGIN, workspaceDevServer } from './dev/mock.ts'
import { thirdPartyNotices } from './build/notices.ts'

// The output in dist/ is embedded into the astrona binary with go:embed and
// served under a strict Content Security Policy (internal/workspace/guard.go):
// default-src 'self', no inline script or style, no data: URIs. The build
// settings below keep the output inside that policy.

/** The policy, read from guard.go so the preview never drifts from it. */
function serverPolicy(): string {
  const guard = readFileSync(fileURLToPath(new URL('../guard.go', import.meta.url)), 'utf8')
  const decl = /const ContentSecurityPolicy = ([^\n]*(?:\n\t[^\n]*)*)/.exec(guard)?.[1]
  const policy = decl ? [...decl.matchAll(/"([^"]*)"/g)].map((m) => m[1]).join('') : ''
  if (!policy.includes("default-src 'self'")) throw new Error('could not read ContentSecurityPolicy from guard.go')
  return policy
}

export default defineConfig(({ command, isPreview }) => ({
  plugins: [vue(), thirdPartyNotices(), ...(command === 'serve' ? [workspaceDevServer()] : [])],
  resolve: {
    alias: {
      '@fonts': fileURLToPath(new URL('./node_modules/@fontsource', import.meta.url)),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Small assets would otherwise become data: URIs, which font-src and
    // img-src 'self' refuse.
    assetsInlineLimit: 0,
    // The preload polyfill is unnecessary for the browsers we support.
    modulePreload: { polyfill: false },
    cssCodeSplit: false,
    sourcemap: false,
    target: 'es2022',
    chunkSizeWarningLimit: 700,
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
  },
  // `npm run preview` serves the built dist/ with the mock under the same
  // policy the Go server sends, so CSP violations show up before release.
  // The server adds frame-src with the origins of the run's page tabs; the
  // mock's page tab is MOCK_APP_ORIGIN.
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true,
    headers: isPreview ? { 'Content-Security-Policy': `${serverPolicy()}; frame-src ${MOCK_APP_ORIGIN}` } : {},
  },
}))
