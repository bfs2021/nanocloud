/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { readFileSync } from 'fs'

const pkg = JSON.parse(readFileSync('./package.json', 'utf8'))

// DAEMON is the local nanocloud the dev server proxies the API to; set
// NANOCLOUD_DAEMON=http://localhost:8080 for one with TLS turned off.
const DAEMON = process.env.NANOCLOUD_DAEMON ?? 'https://localhost:8080'

// Production builds alias ./mock to an empty stub so no mock data ships.
// Dev builds use the real mock, and so does `--mode demo` (see .env.demo),
// built with relative paths into dist-demo so it works under any URL prefix.
export default defineConfig(({ mode }) => ({
  plugins: [react()],
  base: mode === 'demo' ? './' : '/',
  // es2022: noVNC's H.264 capability probe uses top-level await, which
  // Vite's default target (es2020) can't transpile. Both build and dev-time
  // dependency pre-bundling need it set independently.
  build: {
    target: 'es2022',
    outDir: mode === 'demo' ? 'dist-demo' : 'dist',
  },
  optimizeDeps: {
    esbuildOptions: { target: 'es2022' },
  },
  define: {
    // NANOCLOUD_VERSION lets a build outside the release workflow (the
    // demo on GitHub Pages) carry the release it shows, without editing
    // package.json.
    __APP_VERSION__: JSON.stringify(process.env.NANOCLOUD_VERSION?.replace(/^v/, '') || pkg.version),
  },
  resolve: {
    alias: mode === 'production' ? {
      './mock': path.resolve(__dirname, 'src/mock.stub.ts'),
    } : {},
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
  },
  server: {
    // The daemon serves HTTPS with a certificate it signed itself, which the
    // proxy is told to accept (secure: false). ws: true on /vms so the
    // console WebSocket upgrade is proxied too, not just plain requests.
    proxy: Object.fromEntries(
      ['/vms', '/images', '/storage', '/network', '/health', '/system', '/events', '/stats',
        '/auth', '/services', '/templates', '/cloud-init', '/settings']
        .map(path => [path, { target: DAEMON, secure: false, ws: path === '/vms' }]),
    ),
  },
}))
