import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { Sandbox, Template, defaultBuildLogger } from 'e2b';

import { buildSmokeAndPromote } from './template-lifecycle.mjs';
import {
  createBirdBoxTemplate,
  goBuildEnvironment,
  nodeVersion,
  readTemplateBuildConfig,
} from './template.mjs';

const runnerDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(runnerDir, '..');
const { name, promotionTag } = readTemplateBuildConfig();

function git(...args) {
  return execFileSync('git', args, { cwd: repositoryRoot, encoding: 'utf8' }).trim();
}

const dirty = git('status', '--porcelain');
if (dirty && process.env.BIRDY_E2B_ALLOW_DIRTY_BUILD !== '1') {
  throw new Error('refusing to build a production template from a dirty worktree');
}

const version = git('describe', '--tags', '--always', '--dirty');
const commit = git('rev-parse', '--short', 'HEAD');
const builtAt = new Date().toISOString();
const linkerFlags = [
  '-s',
  '-w',
  `-X github.com/guzus/birdy/cmd.version=${version}`,
  `-X github.com/guzus/birdy/cmd.commit=${commit}`,
  `-X github.com/guzus/birdy/cmd.date=${builtAt}`,
].join(' ');

let buildDir;
try {
  buildDir = mkdtempSync(join(tmpdir(), 'bird-box-template-'));
  const binaryPath = join(buildDir, 'birdy');
  execFileSync('go', [
    'build',
    '-trimpath',
    `-ldflags=${linkerFlags}`,
    '-o',
    binaryPath,
    '.',
  ], {
    cwd: repositoryRoot,
    env: goBuildEnvironment(process.env, join(buildDir, 'go-cache')),
    stdio: 'inherit',
  });

  const result = await buildSmokeAndPromote({
    Sandbox,
    Template,
    buildOptions: {
      cpuCount: 2,
      memoryMB: 1024,
      onBuildLogs: defaultBuildLogger(),
    },
    candidateTag: `candidate-${commit}-${Date.now()}`,
    name,
    promotionTag,
    smokeCommand: `set -eu; test "$(id -un)" = user; test "$(pwd)" = /home/user; test "$(uname -m)" = x86_64; test "$(node --version)" = v${nodeVersion}; test -x "$(command -v birdy)"; test -x "$(command -v claude)"; test -x "$(command -v bird)"; npm list -g --depth=0 @anthropic-ai/claude-code@2.1.207 @steipete/bird@0.8.0; birdy version | grep -F ${commit}; claude --version; bird --version`,
    template: createBirdBoxTemplate(buildDir),
  });
  process.stdout.write(
    `Built and promoted ${result.immutableTarget} to ${result.promotionTarget}\n`,
  );
} finally {
  if (buildDir) rmSync(buildDir, { force: true, recursive: true });
}
