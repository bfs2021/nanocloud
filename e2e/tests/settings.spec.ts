import { test, expect } from '@playwright/test';
import { signIn } from './auth';

let api: Record<string, string>;

test.beforeEach(async ({ context }) => {
  await context.route(
    url => url.hostname !== '127.0.0.1' && url.hostname !== 'localhost',
    route => route.abort()
  );
  api = await signIn(context);
});

test('a setting changed in the UI is still set after a reload', async ({ page, context }) => {
  const before = await (await context.request.get('/settings', { headers: api })).json();

  try {
    await page.goto('/#/settings/updates');
    const toggle = page.getByRole('switch', { name: 'Check for new releases' });
    const was = (await toggle.getAttribute('aria-checked')) === 'true';

    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-checked', String(!was));

    // The daemon holds it, not the page.
    await page.reload();
    await expect(page.getByRole('switch', { name: 'Check for new releases' })).toHaveAttribute('aria-checked', String(!was));
  } finally {
    // The suite shares one daemon; leave it as it was found.
    await context.request.patch('/settings', { headers: api, data: { update_check: before.update_check } });
  }
});

test('a newer release shows in the sidebar and leads to the update command', async ({ page }) => {
  // A stub, so the test does not depend on GitHub or on which tag is latest.
  await page.route('**/system/update', route =>
    route.fulfill({
      json: {
        current: '0.1.0',
        available: true,
        latest: { tag: 'v9.9.9', url: 'https://example.invalid/notes', published_at: '2026-11-01T00:00:00Z' },
      },
    })
  );

  await page.goto('/');
  await page.getByRole('complementary').getByRole('button', { name: 'v9.9.9 available' }).click();

  await expect(page).toHaveURL(/#\/settings\/updates$/);
  await expect(page.getByText('sudo nanocloud update')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Release notes' })).toHaveAttribute('href', 'https://example.invalid/notes');
});
