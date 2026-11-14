import { test, expect } from '@playwright/test';
import { E2E_PASSWORD } from './auth';

test('a signed-out browser gets the login page, and the API refuses it', async ({ page, request }) => {
  const api = await request.get('/vms');
  expect(api.status()).toBe(401);

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toHaveCount(0);
});

test('a wrong password is refused and a right one signs in', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Password').fill('not-the-password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('wrong password');

  await page.getByLabel('Password').fill(E2E_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();

  // The session survives a reload: it is a cookie, not page state.
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();
});

test('signing out from the command palette returns to the login page', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Password').fill(E2E_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();

  await page.keyboard.press('ControlOrMeta+k');
  await page.keyboard.type('Sign out');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  const api = await page.request.get('/vms');
  expect(api.status()).toBe(401);
});

test('the session cookie alone signs nothing in', async ({ page, context }) => {
  // Browsers send the cookie to every port on the host, including the
  // service ports forwarded into VMs; it must not be enough on its own.
  await page.goto('/');
  await page.getByLabel('Password').fill(E2E_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();

  const cookies = await context.cookies();
  expect(cookies.map(c => c.name)).toContain('nanocloud_session');
  expect((await context.request.get('/vms')).status()).toBe(401);
});
