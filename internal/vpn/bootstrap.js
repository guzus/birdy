'use strict';

// birdy VPN bootstrap. Loaded via NODE_OPTIONS=--require=<this-file>
// when birdy launches bird with VPN routing enabled.
//
// Node 22+ ships a built-in undici-backed fetch(), but the global
// `fetch` uses Node's INTERNAL undici instance, which doesn't expose a
// way to override its dispatcher from userspace. Setting
// `setGlobalDispatcher` on a userspace `require('undici')` only affects
// THAT instance, not the global fetch.
//
// The workaround: load undici from userspace (BIRDY_UNDICI_PATH —
// populated by `birdy vpn install-deps`), install our ProxyAgent as the
// global dispatcher on that instance, then OVERWRITE globalThis.fetch
// with the userspace undici's fetch. bird's `fetch(...)` calls then
// flow through our user-space fetch, which honors the ProxyAgent.
//
// Verified end-to-end with a mock SOCKS5 + httptest target: with this
// bootstrap, every fetch call routes through HTTPS_PROXY → SOCKS5 →
// target. Without the globalThis override, fetch bypasses the proxy.

const fs = require('node:fs');

const proxyUrl = process.env.HTTPS_PROXY;
if (!proxyUrl) {
  // birdy didn't request VPN routing on this invocation.
  return;
}

const undiciPath = process.env.BIRDY_UNDICI_PATH;
if (!undiciPath) {
  process.stderr.write(
    '[birdy-vpn] BIRDY_UNDICI_PATH not set; this bootstrap must be loaded by birdy --vpn, not directly.\n'
  );
  process.exit(2);
}

if (!fs.existsSync(undiciPath + '/package.json')) {
  process.stderr.write(
    `[birdy-vpn] undici not found at ${undiciPath} — run: birdy vpn install-deps\n`
  );
  process.exit(2);
}

let undici;
try {
  undici = require(undiciPath);
} catch (e) {
  process.stderr.write(
    `[birdy-vpn] failed to load undici from ${undiciPath}: ${e.message}\n` +
    `             (try: rm -rf "${undiciPath}/.." && birdy vpn install-deps)\n`
  );
  process.exit(2);
}

if (
  typeof undici.setGlobalDispatcher !== 'function' ||
  typeof undici.ProxyAgent !== 'function' ||
  typeof undici.fetch !== 'function'
) {
  process.stderr.write(
    `[birdy-vpn] undici at ${undiciPath} is missing required exports — version mismatch?\n` +
    `             (try: birdy vpn install-deps to reinstall)\n`
  );
  process.exit(2);
}

try {
  undici.setGlobalDispatcher(new undici.ProxyAgent(proxyUrl));
} catch (e) {
  process.stderr.write(`[birdy-vpn] failed to install ProxyAgent for ${proxyUrl}: ${e.message}\n`);
  process.exit(2);
}

// CRITICAL: replace Node's built-in fetch with userspace undici's fetch.
// Without this, bird's fetch() ignores our ProxyAgent. See comment block
// above. We also replace Headers/Request/Response so bird gets a
// consistent set of types from the same undici instance.
globalThis.fetch = undici.fetch;
globalThis.Headers = undici.Headers;
globalThis.Request = undici.Request;
globalThis.Response = undici.Response;
globalThis.FormData = undici.FormData;
