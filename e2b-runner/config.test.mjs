import assert from 'node:assert/strict';
import test from 'node:test';

import {
  cancelSafetyBufferMs,
  claudeCommand,
  defaultCancelGraceMs,
  defaultCleanupTimeoutMs,
  defaultCommandTimeoutMs,
  defaultRequestTimeoutMs,
  defaultSandboxTimeoutMs,
  deadlineSafetyBufferMs,
  forwardedEnvs,
  internalBudgetEnvName,
  internalCancelGraceEnvName,
  readConfig,
  sandboxSafetyBufferMs,
  standaloneHTTPTimeoutMs,
  writeChunk,
} from './config.mjs';

test('forwards only Claude and birdy runtime variables', () => {
  const envs = forwardedEnvs({
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: '[{"name":"one"}]',
    BIRDY_HOST_INVITE_CODE: 'invite-secret',
    E2B_API_KEY: 'e2b-secret',
    HOME: '/host/home',
    [internalBudgetEnvName]: '359000',
    [internalCancelGraceEnvName]: '10000',
  });

  assert.deepEqual(envs, {
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: '[{"name":"one"}]',
  });
});

test('uses long-running defaults with a sandbox cleanup buffer', () => {
  const config = readConfig({
    BIRDY_E2B_TEMPLATE: 'bird-box-v1',
    E2B_API_KEY: 'e2b-secret',
  });

  assert.equal(config.commandTimeoutMs, defaultCommandTimeoutMs);
  assert.equal(config.sandboxTimeoutMs, defaultSandboxTimeoutMs);
  assert.equal(config.requestTimeoutMs, defaultRequestTimeoutMs);
  assert.equal(config.cleanupTimeoutMs, defaultCleanupTimeoutMs);
  assert.equal(config.cancelGraceMs, defaultCancelGraceMs);
  assert.equal(config.callerBudgetMs, standaloneHTTPTimeoutMs);
  assert.ok(config.commandTimeoutMs > 60_000);
  assert.ok(
    (2 * config.requestTimeoutMs) + config.commandTimeoutMs + config.cleanupTimeoutMs
      < standaloneHTTPTimeoutMs - deadlineSafetyBufferMs,
  );
  assert.ok(
    config.sandboxTimeoutMs
      >= config.requestTimeoutMs + config.commandTimeoutMs + config.cleanupTimeoutMs
        + sandboxSafetyBufferMs,
  );
});

test('rejects configuration that overruns the caller deadline', () => {
  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'bird-box-v1',
      E2B_API_KEY: 'e2b-secret',
      BIRDY_E2B_COMMAND_TIMEOUT_MS: '283000',
      BIRDY_E2B_REQUEST_TIMEOUT_MS: '30000',
      BIRDY_E2B_SANDBOX_TIMEOUT_MS: '420000',
      [internalBudgetEnvName]: '359000',
    }),
    /caller deadline/,
  );
});

test('uses a caller-provided budget and rejects a shorter context', () => {
  const config = readConfig({
    BIRDY_E2B_TEMPLATE: 'bird-box-v1',
    E2B_API_KEY: 'e2b-secret',
    [internalBudgetEnvName]: '359000',
  });
  assert.equal(config.callerBudgetMs, 359000);

  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'bird-box-v1',
      E2B_API_KEY: 'e2b-secret',
      [internalBudgetEnvName]: '350000',
    }),
    /caller deadline/,
  );
});

test('requires creation and cleanup to fit inside the host cancellation grace', () => {
  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'bird-box-v1',
      E2B_API_KEY: 'e2b-secret',
      [internalCancelGraceEnvName]: String(
        defaultRequestTimeoutMs + defaultCleanupTimeoutMs + cancelSafetyBufferMs,
      ),
    }),
    /fit within the host cancellation grace/,
  );
});

test('rejects a sandbox timeout without command and cleanup headroom', () => {
  assert.throws(
    () => readConfig({
      BIRDY_E2B_TEMPLATE: 'bird-box-v1',
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
