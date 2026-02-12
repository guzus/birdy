#!/usr/bin/env node
import { pathToFileURL } from 'node:url';

function usage() {
  // Keep this short; tokens are sensitive and should not be echoed in help output.
  console.error('Usage: extract_x_tokens.mjs [--format env|json] [--browsers chrome,safari,firefox,edge]');
  console.error('       extract_x_tokens.mjs [--chrome-profile <name-or-path>]');
  console.error('Env: SWEET_COOKIE_MODULE=/path/to/@steipete/sweet-cookie/dist/index.js');
}

function parseArgs(argv) {
  const out = { format: 'env', browsers: undefined, chromeProfile: undefined };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '-h' || a === '--help') {
      out.help = true;
    } else if (a === '--format') {
      out.format = argv[++i] ?? '';
    } else if (a.startsWith('--format=')) {
      out.format = a.slice('--format='.length);
    } else if (a === '--browsers') {
      out.browsers = argv[++i] ?? '';
    } else if (a.startsWith('--browsers=')) {
      out.browsers = a.slice('--browsers='.length);
    } else if (a === '--chrome-profile') {
      out.chromeProfile = argv[++i] ?? '';
    } else if (a.startsWith('--chrome-profile=')) {
      out.chromeProfile = a.slice('--chrome-profile='.length);
    } else {
      console.error(`Unknown arg: ${a}`);
      out.help = true;
    }
  }
  return out;
}

function normalizeBrowsers(raw) {
  if (!raw) return undefined;
  const allowed = new Set(['chrome', 'safari', 'firefox', 'edge']);
  const tokens = raw
    .split(/[,\s]+/)
    .map((t) => t.trim().toLowerCase())
    .filter(Boolean);
  const out = [];
  for (const t of tokens) {
    if (allowed.has(t) && !out.includes(t)) out.push(t);
  }
  return out.length ? out : undefined;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    usage();
    process.exit(2);
  }

  const modulePath = (process.env.SWEET_COOKIE_MODULE || '').trim();
  if (!modulePath) {
    console.error('Missing SWEET_COOKIE_MODULE env var.');
    usage();
    process.exit(2);
  }

  let getCookies;
  try {
    const mod = await import(pathToFileURL(modulePath).href);
    getCookies = mod.getCookies;
  } catch (err) {
    console.error(`Failed to import sweet-cookie from ${modulePath}`);
    console.error(String(err));
    process.exit(1);
  }

  const browsers = normalizeBrowsers(args.browsers);
  const { cookies, warnings } = await getCookies({
    url: 'https://x.com',
    // Only fetch the two required cookies.
    names: ['auth_token', 'ct0'],
    ...(browsers ? { browsers } : {}),
    ...(args.chromeProfile ? { chromeProfile: args.chromeProfile } : {}),
  });

  // Print warnings to stderr (no values).
  for (const w of warnings || []) {
    console.error(`warning: ${w}`);
  }

  const byName = new Map();
  for (const c of cookies || []) {
    if (typeof c?.name === 'string' && typeof c?.value === 'string') {
      if (!byName.has(c.name)) byName.set(c.name, c.value);
    }
  }

  const authToken = byName.get('auth_token') || '';
  const ct0 = byName.get('ct0') || '';

  if (!authToken || !ct0) {
    console.error('error: could not find both auth_token and ct0 from browser cookies.');
    console.error('error: make sure you are logged into x.com, then try a specific browser source via --browsers.');
    process.exit(1);
  }

  if (args.format === 'json') {
    process.stdout.write(JSON.stringify({ auth_token: authToken, ct0 }, null, 2) + '\n');
    return;
  }

  if (args.format !== 'env') {
    console.error(`Unknown --format ${JSON.stringify(args.format)} (expected env|json)`);
    process.exit(2);
  }

  // Output suitable for copy/paste; use single quotes and escape any embedded ones.
  const q = (s) => `'${s.replaceAll("'", "'\"'\"'")}'`;
  process.stdout.write(`export AUTH_TOKEN=${q(authToken)}\n`);
  process.stdout.write(`export CT0=${q(ct0)}\n`);
}

await main();
