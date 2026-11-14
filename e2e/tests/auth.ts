import { expect, type BrowserContext } from '@playwright/test';

// The password playwright.config.ts sets before starting the daemon.
export const E2E_PASSWORD = 'e2e-password';

// signIn logs the context in through the API. context.request shares the
// browser's cookie jar, and the page's half of the session is put where the
// page keeps it, so every page opened afterwards is signed in. It returns the
// header a direct API request needs alongside the cookie.
export async function signIn(context: BrowserContext): Promise<Record<string, string>> {
  const res = await context.request.post('/auth/login', { data: { password: E2E_PASSWORD } });
  expect(res.status(), await res.text()).toBe(200);
  const { key } = (await res.json()) as { key: string };
  await context.addInitScript(k => localStorage.setItem('nanocloud.session-key', k), key);
  return { 'X-Nanocloud-Key': key };
}
