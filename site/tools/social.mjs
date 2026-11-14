// Renders social.html to site/social.png, the preview shown when the site or
// the repository is linked. Needs e2e's Playwright: (cd e2e && npm ci).
import { fileURLToPath } from 'node:url';
import { chromium } from '../../e2e/node_modules/playwright/index.mjs';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 640 } });
await page.goto(new URL('./social.html', import.meta.url).href);
await page.evaluate(() => document.fonts.ready);
await page.screenshot({ path: fileURLToPath(new URL('../social.png', import.meta.url)) });
await browser.close();
console.log('social.png');
