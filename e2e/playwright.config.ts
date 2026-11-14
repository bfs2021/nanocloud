import { readFileSync } from 'node:fs';
import { defineConfig, devices } from '@playwright/test';

// Boots the real compiled binary (embedded UI + libvirt test driver) and runs
// the smoke suite against it. Build first: `make all` at the repo root.

// Read the port from config.e2e.yaml so the two files can't drift.
const configYaml = readFileSync(new URL('./config.e2e.yaml', import.meta.url), 'utf-8');
const port = configYaml.match(/^port:\s*(\d+)/m)?.[1];
if (!port) {
  throw new Error('config.e2e.yaml must set a top-level "port:"');
}
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './tests',
  // The suite mutates shared server state (starts/stops the test domain),
  // so specs must not run concurrently.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // A browser, a fresh libvirtd and the compiled binary together make this the
  // flakiest release gate, so CI retries twice.
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    // The password is set before the daemon starts: with none, it refuses
    // every request. tests/auth.ts signs each test in with it.
    command: 'sh -c "mkdir -p /tmp/nanocloud-e2e/pool /tmp/nanocloud-e2e/images && echo e2e-password | ../nanocloud passwd -config config.e2e.yaml -stdin && exec ../nanocloud serve -config config.e2e.yaml"',
    url: `${baseURL}/health`,
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
