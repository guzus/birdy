import assert from 'node:assert/strict';
import test from 'node:test';

import { buildSmokeAndPromote } from './template-lifecycle.mjs';

function fakes({ buildError, createError, exitCode = 0, killError } = {}) {
  const calls = [];
  const Template = {
    async build(_template, target, options) {
      calls.push(['build', target, options]);
      if (buildError) throw buildError;
      return { buildId: 'immutable-build-id' };
    },
    async assignTags(target, tag) {
      calls.push(['promote', target, tag]);
    },
  };
  const sandbox = {
    commands: {
      async run(command, options) {
        calls.push(['run', command, options]);
        return { exitCode };
      },
    },
    async kill(options) {
      calls.push(['kill', options]);
      if (killError) throw killError;
    },
  };
  const Sandbox = {
    async create(target, options) {
      calls.push(['create', target, options]);
      if (createError) throw createError;
      return sandbox;
    },
  };
  return { calls, Sandbox, Template };
}

function run(failures) {
  const fake = fakes(failures);
  return {
    ...fake,
    promise: buildSmokeAndPromote({
      Sandbox: fake.Sandbox,
      Template: fake.Template,
      buildOptions: { cpuCount: 2 },
      candidateTag: 'candidate-abc-123',
      name: 'bird-box',
      promotionTag: 'production',
      smokeCommand: 'smoke',
      template: { definition: true },
    }),
  };
}

test('builds a candidate, smokes its build ID, cleans up, then promotes', async () => {
  const { calls, promise } = run();
  assert.deepEqual(await promise, {
    immutableTarget: 'bird-box:immutable-build-id',
    promotionTarget: 'bird-box:production',
  });
  assert.deepEqual(calls.map(call => call[0]), ['build', 'create', 'run', 'kill', 'promote']);
  assert.equal(calls[0][1], 'bird-box:candidate-abc-123');
  assert.equal(calls[1][1], 'bird-box:immutable-build-id');
  assert.deepEqual(calls[4].slice(1), ['bird-box:immutable-build-id', 'production']);
});

test('never promotes when candidate build or sandbox creation fails', async () => {
  for (const failures of [
    { buildError: new Error('build failed') },
    { createError: new Error('create failed') },
  ]) {
    const { calls, promise } = run(failures);
    await assert.rejects(promise);
    assert.equal(calls.some(call => call[0] === 'promote'), false);
  }
});

test('kills but never promotes when the smoke command fails', async () => {
  const { calls, promise } = run({ exitCode: 7 });
  await assert.rejects(promise, /status 7/);
  assert.deepEqual(calls.map(call => call[0]), ['build', 'create', 'run', 'kill']);
});

test('never promotes when cleanup fails', async () => {
  const { calls, promise } = run({ killError: new Error('kill failed') });
  await assert.rejects(promise, /kill failed/);
  assert.deepEqual(calls.map(call => call[0]), ['build', 'create', 'run', 'kill']);
});
