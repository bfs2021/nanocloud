// Takes the site's screenshots from the demo build, so they match the current UI.
//
//   (cd web && npm run build:demo) && (cd e2e && npm ci) && node site/tools/screenshots.mjs
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '../../e2e/node_modules/playwright/index.mjs';

const root = fileURLToPath(new URL('../../web/dist-demo/', import.meta.url));
const out = fileURLToPath(new URL('../screens/', import.meta.url));
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.json': 'application/json', '.woff2': 'font/woff2' };

const server = createServer(async (req, res) => {
  const path = normalize(decodeURIComponent(new URL(req.url, 'http://x').pathname)).replace(/^(\.\.[/\\])+/, '');
  try {
    const body = await readFile(join(root, path.endsWith('/') ? path + 'index.html' : path));
    res.writeHead(200, { 'Content-Type': types[extname(path)] ?? 'text/html' }).end(body);
  } catch {
    res.writeHead(404).end();
  }
}).listen(0);
const base = `http://localhost:${server.address().port}/`;

// name, route, theme, what to do once the page is up
const shots = [
  ['dashboard', '#/vms', 'dark'],
  ['dashboard-light', '#/vms', 'light'],
  ['service', '#/services/k8s-lab', 'dark'],
  ['vm', '#/vms/vm2', 'dark'],
  ['create', '#/vms', 'dark', async p => {
    await p.getByRole('button', { name: /New VM/ }).first().click();
    await p.getByText('Ubuntu 24.04 LTS').first().click();
  }],
  ['settings', '#/settings/updates', 'dark'],
];

// The hero's screens: each page without its sidebar, 16:10, so it can be
// shown at a size where its labels still read.
const heroClip = { x: 250, y: 0, width: 1190, height: 744 };

// Whole panels at their true size, for the column beside each feature: name,
// route, tab to open, the panel, and optionally where to stop it. A narrow
// window lays each panel out at the width the site shows it, so nothing is
// cut off mid-row; a tall one keeps every panel on screen without scrolling.
const cropViewport = { width: 820, height: 1600 };
const crops = [
  ['crop-recipe', '#/services', null, p => p.getByRole('heading', { name: 'PostgreSQL', exact: true }).locator('xpath=ancestor::div[contains(@class,"nd-lift")][1]')],
  ['crop-machines', '#/services/k8s-lab', null, p => p.getByText('Machines (3)', { exact: true }).locator('xpath=ancestor::section[1]')],
  ['crop-snapshots', '#/vms/vm1', 'Storage', p => p.getByText('Snapshots', { exact: true }).first().locator('xpath=ancestor::div[3]')],
  ['crop-devices', '#/vms/vm2', 'Hardware', p => p.getByRole('heading', { name: 'Passthrough devices' }).locator('xpath=ancestor::div[1]'), p => p.getByText(/devices this host cannot release/)],
];

const browser = await chromium.launch();
for (const [name, route, theme, act] of shots) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
  await page.addInitScript(t => localStorage.setItem('nanocloud.theme', t), theme);
  await page.goto(base + route);
  await page.addStyleTag({ content: '[data-demo-banner]{display:none!important}' });
  // Long enough for the stats to be sampled and the charts to fill.
  await page.waitForTimeout(6000);
  if (act) await act(page);
  await page.waitForTimeout(600);
  await page.screenshot({ path: join(out, `${name}.jpg`), type: 'jpeg', quality: 82 });
  if (['dashboard', 'service', 'create'].includes(name)) await page.screenshot({ path: join(out, `hero-${name}.jpg`), type: 'jpeg', quality: 84, clip: heroClip });
  await page.close();
  console.log(`screens/${name}.jpg`);
}
for (const [name, route, tab, panel, stop] of crops) {
  const page = await browser.newPage({ viewport: cropViewport, deviceScaleFactor: 2 });
  await page.addInitScript(() => localStorage.setItem('nanocloud.theme', 'dark'));
  await page.goto(base + route);
  await page.addStyleTag({ content: '[data-demo-banner]{display:none!important}' });
  await page.waitForTimeout(2500);
  if (tab) await page.getByRole('tab', { name: tab }).click();
  await page.waitForTimeout(600);
  const clip = await panel(page).boundingBox();
  if (stop) {
    const end = await stop(page).boundingBox();
    clip.height = end.y + end.height + 16 - clip.y;
  }
  await page.screenshot({ path: join(out, `${name}.jpg`), type: 'jpeg', quality: 86, clip, fullPage: true });
  await page.close();
  console.log(`screens/${name}.jpg ${Math.round(clip.width)}x${Math.round(clip.height)}`);
}
await browser.close();
server.close();
