import { Template } from 'e2b';

export const defaultTemplateName = 'bird-box';
export const defaultPromotionTag = 'production';
export const nodeVersion = '22.23.1';

const nodeArchive = `node-v${nodeVersion}-linux-x64.tar.xz`;
const nodeArchiveSHA256 = '9749e988f437343b7fa832c69ded82a312e41a03116d766797ac14f6f9eee578';

const templateNamePattern = /^[a-z0-9][a-z0-9_-]*$/;
const templateTagPattern = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/;

export function readTemplateBuildConfig(env = process.env) {
  const name = env.BIRDY_E2B_TEMPLATE_NAME?.trim() || defaultTemplateName;
  const promotionTag = env.BIRDY_E2B_PROMOTION_TAG?.trim() || defaultPromotionTag;
  if (!templateNamePattern.test(name)) {
    throw new Error('BIRDY_E2B_TEMPLATE_NAME must be a lowercase E2B template name');
  }
  if (!templateTagPattern.test(promotionTag)) {
    throw new Error('BIRDY_E2B_PROMOTION_TAG must be a valid E2B template tag');
  }
  return { name, promotionTag };
}

export function goBuildEnvironment(env, cachePath) {
  const inheritedNames = [
    'GOMODCACHE',
    'GONOPROXY',
    'GONOSUMDB',
    'GOPATH',
    'GOPRIVATE',
    'GOPROXY',
    'GOSUMDB',
    'GOTOOLCHAIN',
    'HOME',
    'PATH',
    'SSL_CERT_DIR',
    'SSL_CERT_FILE',
    'TMPDIR',
  ];
  const inherited = Object.fromEntries(
    inheritedNames.filter(name => env[name] !== undefined).map(name => [name, env[name]]),
  );
  return {
    ...inherited,
    CGO_ENABLED: '0',
    GOARCH: 'amd64',
    GOCACHE: cachePath,
    GOOS: 'linux',
  };
}

export function createBirdBoxTemplate(binaryContextPath) {
  return Template({ fileContextPath: binaryContextPath })
    .fromBaseImage()
    .setUser('root')
    .aptInstall(['ca-certificates', 'curl', 'xz-utils'], { noInstallRecommends: true })
    .runCmd([
      'test "$(dpkg --print-architecture)" = amd64',
      `curl -fsSLo /tmp/${nodeArchive} https://nodejs.org/dist/v${nodeVersion}/${nodeArchive}`,
      `echo "${nodeArchiveSHA256}  /tmp/${nodeArchive}" | sha256sum -c -`,
      'rm -rf /usr/local/lib/node_modules/npm',
      'rm -f /usr/local/bin/node /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack',
      `tar -xJf /tmp/${nodeArchive} -C /usr/local --strip-components=1 --no-same-owner`,
      `rm /tmp/${nodeArchive}`,
      `test "$(node --version)" = v${nodeVersion}`,
    ])
    .copy('birdy', '/usr/local/bin/birdy', { mode: 0o755, user: 'root' })
    .npmInstall([
      '@anthropic-ai/claude-code@2.1.207',
      '@steipete/bird@0.8.0',
    ], { g: true })
    .setUser('user')
    .setWorkdir('/home/user')
    .runCmd([
      'test "$(id -un)" = user',
      'test "$(pwd)" = /home/user',
      'test "$(uname -m)" = x86_64',
      `test "$(node --version)" = v${nodeVersion}`,
      'test -x "$(command -v birdy)"',
      'test -x "$(command -v claude)"',
      'test -x "$(command -v bird)"',
      'npm list -g --depth=0 @anthropic-ai/claude-code@2.1.207 @steipete/bird@0.8.0',
      'birdy version',
      'claude --version',
      'bird --version',
    ]);
}
