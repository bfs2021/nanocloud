// Fails when the production bundle contains the mock data, which vite.config.ts
// swaps for an empty stub in production builds. Run after `npm run build`.
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Strings that appear only in src/mock.ts.
const SENTINELS = ['web-prod-01', 'edge-cache', '3A:91:5F:0C:7E:B2'];

const dir = new URL('../dist/assets/', import.meta.url).pathname;
const bundles = readdirSync(dir).filter(f => f.endsWith('.js'));
if (bundles.length === 0) {
  console.error(`no JavaScript in ${dir}; run npm run build first`);
  process.exit(1);
}

let leaked = false;
for (const f of bundles) {
  const text = readFileSync(join(dir, f), 'utf8');
  for (const s of SENTINELS.filter(s => text.includes(s))) {
    console.error(`${f} contains mock data: "${s}"`);
    leaked = true;
  }
}
if (leaked) process.exit(1);
console.log(`no mock data in ${bundles.length} bundle(s)`);
