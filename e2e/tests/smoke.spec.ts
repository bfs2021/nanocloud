import { test, expect, type Page } from '@playwright/test';
import { signIn } from './auth';

// Smoke suite against the real binary on libvirt's test:///default driver.
// The driver's one pre-seeded domain, "test", proves the UI → API → libvirtd
// path, since the server shows an empty list when libvirt is unreachable.

// The libvirt test-driver domain, present in every fresh connection.
const TEST_DOMAIN = 'test';

// The UI references Google Fonts, which can stall the load event offline.
// Abort anything that isn't the local server.
test.beforeEach(async ({ context }) => {
  await context.route(
    url => url.hostname !== '127.0.0.1' && url.hostname !== 'localhost',
    route => route.abort()
  );
  await signIn(context);
});

async function openDashboard(page: Page) {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();
}

test('app shell loads without uncaught errors', async ({ page }) => {
  const errors: Error[] = [];
  page.on('pageerror', err => errors.push(err));

  await openDashboard(page);
  await expect(page.getByRole('img', { name: 'Nano Cloud' })).toBeVisible();

  expect(errors, `uncaught page errors: ${errors.map(e => e.message).join('; ')}`).toHaveLength(0);
});

test('dashboard lists the libvirt test domain as running', async ({ page }) => {
  await openDashboard(page);

  // An empty dashboard means the server fell back to libvirt-less mode.
  await expect(page.getByText(TEST_DOMAIN, { exact: true })).toBeVisible();
  // Only running VMs get a Stop action.
  await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
});

test('VM lifecycle: stop and start the test domain through the UI', async ({ page }) => {
  await openDashboard(page);
  // isVisible() doesn't auto-wait, and the card renders after /vms resolves.
  await expect(page.getByText(TEST_DOMAIN, { exact: true })).toBeVisible();

  // Read the power state rather than assume it: a failed earlier attempt may
  // have left the domain stopped.
  const running = await page.getByRole('button', { name: 'Stop' }).isVisible();

  async function toggle(action: 'stop' | 'start') {
    const label = action === 'stop' ? 'Stop' : 'Start';
    await Promise.all([
      // click() doesn't wait for the fetch, and an immediate reload would abort it.
      page.waitForResponse(res => res.url().endsWith(`/vms/${TEST_DOMAIN}/${action}`) && res.ok()),
      page.getByRole('button', { name: label }).click(),
    ]);
    // Reload to defeat the optimistic status update and read real libvirt state.
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Virtual machines', exact: true })).toBeVisible();
  }

  if (running) {
    await toggle('stop');
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible();
    await toggle('start');
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
  } else {
    await toggle('start');
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
    await toggle('stop');
    await expect(page.getByRole('button', { name: 'Start' })).toBeVisible();
  }
});

test('images view renders the built-in catalog', async ({ page }) => {
  await openDashboard(page);
  await page.getByRole('button', { name: 'Images' }).click();

  await expect(page.getByRole('heading', { name: 'Images' })).toBeVisible();
  await expect(page.getByText('Ubuntu 24.04 LTS')).toBeVisible();
  // Nothing is cached in the e2e environment, so every entry is pullable.
  expect(await page.getByRole('button', { name: 'Pull' }).count()).toBeGreaterThan(0);
});

test('services view lists the catalog and opens the launch drawer', async ({ page }) => {
  await openDashboard(page);
  await page.getByRole('button', { name: 'Services', exact: true }).click();

  await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible();
  for (const name of ['Kubernetes', 'Kubernetes cluster', 'MySQL', 'PostgreSQL', 'Prometheus + Grafana', 'S3 storage']) {
    await expect(page.getByRole('button', { name: `Launch ${name}`, exact: true })).toBeVisible();
  }
  await page.getByRole('button', { name: 'Launch PostgreSQL', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Launch PostgreSQL' });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByLabel('Name')).toHaveValue('pg-1');
  // Cancel rather than launch: a launch would download an image and build a
  // VM, which the libvirt test driver cannot run.
  await drawer.getByRole('button', { name: 'Cancel' }).click();
  await expect(drawer).not.toBeVisible();

  // Every recipe's drawer opens, including one with no settings.
  for (const name of ['Kubernetes', 'Kubernetes cluster', 'MySQL', 'Prometheus + Grafana', 'S3 storage']) {
    await page.getByRole('button', { name: `Launch ${name}`, exact: true }).click();
    const d = page.getByRole('dialog', { name: `Launch ${name}` });
    await expect(d).toBeVisible();
    await d.getByRole('button', { name: 'Cancel' }).click();
    await expect(d).not.toBeVisible();
  }
});

test('create-VM wizard walks every step to a Review that matches the choices', async ({ page }) => {
  await openDashboard(page);
  await page.getByRole('complementary').getByRole('button', { name: 'New VM' }).click();

  await expect(page.getByText('Create virtual machine')).toBeVisible();
  const next = page.getByRole('button', { name: 'Continue' });
  await expect(next).toBeDisabled();

  await page.getByText('Ubuntu 24.04 LTS').click();
  await expect(next).toBeEnabled();
  await next.click();

  await page.getByLabel('VM name').fill('e2e-first');
  await next.click(); // to Access
  await expect(page.getByLabel('Username')).toBeVisible();
  await next.click(); // to Network
  await next.click(); // to Review

  const review = (section: string) => page.getByRole('region', { name: section });
  await expect(review('Image')).toContainText('Ubuntu 24.04 LTS');
  await expect(review('Resources')).toContainText('e2e-first');
  await expect(review('Resources')).toContainText('e2e-pool');

  // Edit goes to the step and back again in one click each.
  await page.getByRole('button', { name: 'Edit resources' }).click();
  await page.getByLabel('VM name').fill('e2e-second');
  await page.getByRole('button', { name: 'Back to review' }).click();
  await expect(review('Resources')).toContainText('e2e-second');
  await expect(page.getByRole('button', { name: 'Create VM' })).toBeEnabled();

  // Do not submit: the test driver cannot create KVM domains, and the image
  // is not downloaded.
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByText('Create virtual machine')).not.toBeVisible();
});
