import assert from 'node:assert/strict';
import test from 'node:test';

import {
  claudeCommand,
  defaultCleanupTimeoutMs,
  defaultCommandTimeoutMs,
  defaultRequestTimeoutMs,
  defaultSandboxTimeoutMs,
  forwardedEnvs,
  hostedHTTPTimeoutMs,
  hostedSafetyBufferMs,
  readConfig,
  sandboxSafetyBufferMs,
  writeChunk,
} from './config.mjs';

test('forwards only Claude and birdy runtime variables', () => {
  const envs = forwardedEnvs({
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: '[{"name":"one"}]',
    BIRDY_HOST_INVITE_CODE: 'invite-secret',
    E2B_API_KEY: 'e2b-secret',
    HOME: '/host/home',
  });

  assert.deepEqual(envs, {
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: '[{"name":"one"}]',
  });
});

test('uses long-running defaults with a sandbox cleanup buffer', () => {
  const config = readConfig({
    BIRDY_E2B_TEMPLATE: 'birdy-claude-v1',
    E2B_API_KEY: 'e2b-secret',
  });

  assert.equal(config.commandTimeoutMs, defaultCommandTimeoutMs);
  assert.equal(config.sandboxTimeoutMs, defaultSandboxTimeoutMs);
  assert.equal(config.requestTimeoutMs, defaultRequestTimeoutMs);
  assert.equal(config.cleanupTimeoutMs, defaultCleanupTimeoutMs);
  assert.ok(config.commandTimeoutMs > 60_000);
  assert.ok(
    (2 * config.requestTimeoutMs) + config.commandTimeoutMs + config.cleanupTimeoutMs
      <= hostedHTTPTimeoutMs - hostedSafetyBufferMs,
  );
  assert.ok(
    config.sandboxTimeoutMs
      >= config.requestTimeoutMs + config.commandTimeoutMs + config.cleanupTimeoutMs
        + sandboxSafetyBufferMs,
  );
});

test('rejects configuration that overruns the hosted HTTP deadline', () => {
  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'birdy-claude-v1',
      E2B_API_KEY: 'e2b-secret',
      BIRDY_E2B_COMMAND_TIMEOUT_MS: '283000',
      BIRDY_E2B_REQUEST_TIMEOUT_MS: '30000',
      BIRDY_E2B_SANDBOX_TIMEOUT_MS: '420000',
    }),
    /hosted HTTP deadline/,
  );
});

test('rejects a sandbox timeout without command and cleanup headroom', () => {
  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'birdy-claude-v1',
      E2B_API_KEY: 'e2b-secret',
      BIRDY_E2B_COMMAND_TIMEOUT_MS: '280000',
      BIRDY_E2B_REQUEST_TIMEOUT_MS: '30000',
      BIRDY_E2B_SANDBOX_TIMEOUT_MS: '319999',
    }),
    /startup, execution, and cleanup/,
  );
});

test('quotes Claude arguments without shell interpolation', () => {
  const command = claudeCommand(['-p', "what's $(whoami)", '--model', 'sonnet']);
  assert.equal(command, "'claude' '-p' 'what'\"'\"'s $(whoami)' '--model' 'sonnet'");
});

test('preserves output chunks without inserting JSONL delimiters', () => {
  let output = '';
  const stream = { write: chunk => { output += chunk; } };

  writeChunk(stream, 'data: {"type":"assis');
  writeChunk(stream, 'tant"}\n');

  assert.equal(output, 'data: {"type":"assistant"}\n');
});
