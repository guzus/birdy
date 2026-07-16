import assert from 'node:assert/strict';
import test from 'node:test';

import { Template } from 'e2b';

import {
  createBirdBoxTemplate,
  defaultPromotionTag,
  defaultTemplateName,
  goBuildEnvironment,
  nodeVersion,
  readTemplateBuildConfig,
} from './template.mjs';

test('uses the production Bird Box build configuration by default', () => {
  assert.deepEqual(readTemplateBuildConfig({}), {
    name: defaultTemplateName,
    promotionTag: defaultPromotionTag,
  });
  assert.deepEqual(readTemplateBuildConfig({
    BIRDY_E2B_PROMOTION_TAG: ' staging ',
    BIRDY_E2B_TEMPLATE_NAME: ' custom-box ',
  }), { name: 'custom-box', promotionTag: 'staging' });
});

test('rejects malformed template names and tags before building', () => {
  for (const name of ['Bird Box', 'bird-box:production', 'team/bird-box', '--bird-box']) {
    assert.throws(
      () => readTemplateBuildConfig({ BIRDY_E2B_TEMPLATE_NAME: name }),
      /template name/,
    );
  }
  assert.throws(
    () => readTemplateBuildConfig({ BIRDY_E2B_PROMOTION_TAG: 'bad tag' }),
    /template tag/,
  );
});

test('passes only build-related environment to the Go compiler', () => {
  const env = goBuildEnvironment({
    ANTHROPIC_API_KEY: 'anthropic-secret',
    BIRDY_ACCOUNTS: 'x-secret',
    E2B_API_KEY: 'e2b-secret',
    GOPROXY: 'https://proxy.golang.org,direct',
    HOME: '/home/builder',
    PATH: '/usr/bin',
  }, '/tmp/go-cache');

  assert.deepEqual(env, {
    GOPROXY: 'https://proxy.golang.org,direct',
    HOME: '/home/builder',
    PATH: '/usr/bin',
    CGO_ENABLED: '0',
    GOARCH: 'amd64',
    GOCACHE: '/tmp/go-cache',
    GOOS: 'linux',
  });
});

test('bakes Birdy and pinned agent CLIs into the E2B base image', () => {
  const dockerfile = Template.toDockerfile(createBirdBoxTemplate('/tmp/bird-box-test'));

  assert.match(dockerfile, /^FROM e2bdev\/base$/m);
  assert.match(dockerfile, new RegExp(`node-v${nodeVersion}-linux-x64`));
  assert.match(dockerfile, /sha256sum -c/);
  assert.match(dockerfile, /rm -rf \/usr\/local\/lib\/node_modules\/npm/);
  assert.match(dockerfile, /rm -f \/usr\/local\/bin\/node \/usr\/local\/bin\/npm/);
  assert.match(dockerfile, /COPY birdy \/usr\/local\/bin\/birdy/);
  assert.match(dockerfile, /@anthropic-ai\/claude-code@2\.1\.207/);
  assert.match(dockerfile, /@steipete\/bird@0\.8\.0/);
  assert.match(dockerfile, /USER user\nWORKDIR \/home\/user/);
  assert.match(dockerfile, /test -x "\$\(command -v birdy\)"/);
  assert.match(dockerfile, /test -x "\$\(command -v claude\)"/);
  assert.match(dockerfile, /test -x "\$\(command -v bird\)"/);
});
