import assert from 'node:assert/strict';
import test from 'node:test';

import { createBirdBoxLifecycle, installSignalHandlers } from './lifecycle.mjs';

const baseEnv = {
  BIRDY_E2B_TEMPLATE: 'bird-box-v1',
  E2B_API_KEY: 'e2b-secret',
  ANTHROPIC_API_KEY: 'anthropic-secret',
  BIRDY_ACCOUNTS: '[{"name":"one"}]',
  BIRDY_HOST_INVITE_CODE: 'must-not-forward',
};

function captureStream() {
  let value = '';
  return {
    stream: { write: chunk => { value += String(chunk); } },
    value: () => value,
  };
}

function fakeSandbox(options = {}) {
  const calls = { create: [], run: [], kill: [] };
  const sandbox = {
    commands: {
      run: async (command, runOptions) => {
        calls.run.push({ command, options: runOptions });
        runOptions.onStdout('data: {"type":"assis');
        runOptions.onStdout('tant"}\n');
        runOptions.onStderr('remote warning\n');
        if (options.runError) throw options.runError;
        return { exitCode: options.exitCode ?? 0 };
      },
    },
    kill: async killOptions => {
      calls.kill.push(killOptions);
      if (options.killError) throw options.killError;
    },
  };
  const Sandbox = {
    create: async (template, createOptions) => {
      calls.create.push({ template, options: createOptions });
      if (options.createError) throw options.createError;
      return sandbox;
    },
  };
  return { Sandbox, calls };
}

function lifecycleFor(fake, overrides = {}) {
  const stdout = captureStream();
  const stderr = captureStream();
  return {
    lifecycle: createBirdBoxLifecycle({
      Sandbox: fake.Sandbox,
      argv: ['-p', 'hello', '--model', 'sonnet'],
      env: baseEnv,
      stdout: stdout.stream,
      stderr: stderr.stream,
      ...overrides,
    }),
    stdout,
    stderr,
  };
}

test('creates, streams, runs, and cleans up with the exact contract', async () => {
  const fake = fakeSandbox();
  const { lifecycle, stdout, stderr } = lifecycleFor(fake);

  assert.equal(await lifecycle.run(), 0);
  assert.deepEqual(fake.calls.create, [{
    template: 'bird-box-v1',
    options: {
      apiKey: 'e2b-secret',
      metadata: { application: 'birdy-web', runtime: 'claude-code' },
      requestTimeoutMs: 30000,
      secure: true,
      timeoutMs: 420000,
    },
  }]);
  assert.equal(fake.calls.run.length, 1);
  assert.equal(fake.calls.run[0].command, "'claude' '-p' 'hello' '--model' 'sonnet'");
  assert.deepEqual(fake.calls.run[0].options.envs, {
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: '[{"name":"one"}]',
  });
  assert.equal(stdout.value(), 'data: {"type":"assistant"}\n');
  assert.equal(stderr.value(), 'remote warning\n');
  assert.deepEqual(fake.calls.kill, [{ requestTimeoutMs: 8000 }]);
});

test('reports command failure and still kills exactly once', async () => {
  const fake = fakeSandbox({ exitCode: 7 });
  const { lifecycle, stderr } = lifecycleFor(fake);

  assert.equal(await lifecycle.run(), 1);
  assert.equal(fake.calls.kill.length, 1);
  assert.match(stderr.value(), /Claude exited with status 7/);
});

test('handles configuration and create failures without invalid cleanup', async () => {
  for (const scenario of [
    { env: { E2B_API_KEY: 'key' }, expected: /BIRDY_E2B_TEMPLATE is required/ },
    { env: baseEnv, createError: new Error('create failed'), expected: /create failed/ },
  ]) {
    const fake = fakeSandbox({ createError: scenario.createError });
    const { lifecycle, stderr } = lifecycleFor(fake, { env: scenario.env });

    assert.equal(await lifecycle.run(), 1);
    assert.equal(fake.calls.kill.length, 0);
    assert.match(stderr.value(), scenario.expected);
  }
});

test('cleanup failure is controlled and makes the run fail', async () => {
  const fake = fakeSandbox({ killError: new Error('kill failed') });
  const { lifecycle, stderr } = lifecycleFor(fake);

  assert.equal(await lifecycle.run(), 1);
  assert.equal(fake.calls.kill.length, 1);
  assert.match(stderr.value(), /failed to kill E2B sandbox: kill failed/);
});

test('shutdown during creation waits, skips the command, and kills once', async () => {
  let finishCreate;
  const created = fakeSandbox();
  const originalCreate = created.Sandbox.create;
  created.Sandbox.create = async (...args) => {
    await new Promise(resolve => { finishCreate = resolve; });
    return originalCreate(...args);
  };
  const { lifecycle } = lifecycleFor(created);

  const runPromise = lifecycle.run();
  await new Promise(resolve => setImmediate(resolve));
  const shutdownPromise = lifecycle.shutdown();
  finishCreate();

  assert.equal(await shutdownPromise, 0);
  assert.equal(await runPromise, 0);
  assert.equal(created.calls.run.length, 0);
  assert.equal(created.calls.kill.length, 1);
});

test('SIGTERM performs shutdown once and exits with signal status', async () => {
  const handlers = new Map();
  const exits = [];
  let shutdownCalls = 0;
  const processLike = {
    once: (signal, handler) => handlers.set(signal, handler),
    exit: code => exits.push(code),
  };
  const lifecycle = {
    shutdown: async () => {
      shutdownCalls++;
      return 0;
    },
  };
  const isHandlingSignal = installSignalHandlers(processLike, lifecycle);

  handlers.get('SIGTERM')();
  handlers.get('SIGINT')();
  await new Promise(resolve => setImmediate(resolve));

  assert.equal(isHandlingSignal(), true);
  assert.equal(shutdownCalls, 1);
  assert.deepEqual(exits, [143]);
});
