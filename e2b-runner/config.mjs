const forwardedEnvironmentNames = [
  'CLAUDE_CODE_OAUTH_TOKEN',
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_AUTH_TOKEN',
  'ANTHROPIC_BASE_URL',
  'ANTHROPIC_CUSTOM_HEADERS',
  'BIRDY_ACCOUNTS',
  'BIRDY_READ_ONLY',
];

// Budget two control-plane handshakes (sandbox creation and command start),
// command execution, and cleanup inside the caller-provided deadline. The
// six-minute value is only a fallback for running this helper standalone.
export const internalBudgetEnvName = 'BIRDY_INTERNAL_BIRD_BOX_BUDGET_MS';
export const internalCancelGraceEnvName = 'BIRDY_INTERNAL_BIRD_BOX_CANCEL_GRACE_MS';
export const standaloneHTTPTimeoutMs = 360_000;
export const defaultCancelGraceMs = 45_000;
export const cancelSafetyBufferMs = 5_000;
export const deadlineSafetyBufferMs = 10_000;
export const sandboxSafetyBufferMs = 2_000;
export const defaultCleanupTimeoutMs = 8_000;
export const defaultCommandTimeoutMs = 280_000;
export const defaultSandboxTimeoutMs = 420_000;
export const defaultRequestTimeoutMs = 30_000;

export function readPositiveInteger(env, name, fallback) {
  const value = env[name];
  if (value === undefined || value.trim() === '') return fallback;

  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive integer in milliseconds`);
  }
  return parsed;
}

export function readConfig(env = process.env) {
  const template = env.BIRDY_E2B_TEMPLATE?.trim();
  if (!template) throw new Error('BIRDY_E2B_TEMPLATE is required');
  if (!env.E2B_API_KEY?.trim()) throw new Error('E2B_API_KEY is required');

  const commandTimeoutMs = readPositiveInteger(
    env,
    'BIRDY_E2B_COMMAND_TIMEOUT_MS',
    defaultCommandTimeoutMs,
  );
  const sandboxTimeoutMs = readPositiveInteger(
    env,
    'BIRDY_E2B_SANDBOX_TIMEOUT_MS',
    defaultSandboxTimeoutMs,
  );
  const requestTimeoutMs = readPositiveInteger(
    env,
    'BIRDY_E2B_REQUEST_TIMEOUT_MS',
    defaultRequestTimeoutMs,
  );
  const cleanupTimeoutMs = defaultCleanupTimeoutMs;
  const callerBudgetMs = readPositiveInteger(
    env,
    internalBudgetEnvName,
    standaloneHTTPTimeoutMs,
  );
  const cancelGraceMs = readPositiveInteger(
    env,
    internalCancelGraceEnvName,
    defaultCancelGraceMs,
  );

  if (requestTimeoutMs + cleanupTimeoutMs + cancelSafetyBufferMs >= cancelGraceMs) {
    throw new Error('E2B create, cleanup, and scheduling margin must fit within the host cancellation grace');
  }

  const hostedBudgetMs = (2 * requestTimeoutMs) + commandTimeoutMs + cleanupTimeoutMs;
  if (hostedBudgetMs + deadlineSafetyBufferMs >= callerBudgetMs) {
    throw new Error('E2B timeout budget must fit within the caller deadline with cleanup headroom');
  }

  const sandboxBudgetMs = requestTimeoutMs + commandTimeoutMs + cleanupTimeoutMs
    + sandboxSafetyBufferMs;
  if (sandboxTimeoutMs < sandboxBudgetMs) {
    throw new Error('BIRDY_E2B_SANDBOX_TIMEOUT_MS must cover command startup, execution, and cleanup');
  }

  return {
    template,
    commandTimeoutMs,
    sandboxTimeoutMs,
    requestTimeoutMs,
    cleanupTimeoutMs,
    callerBudgetMs,
    cancelGraceMs,
  };
}

export function forwardedEnvs(env = process.env) {
  return Object.fromEntries(
    forwardedEnvironmentNames
      .filter(name => env[name] !== undefined && env[name] !== '')
      .map(name => [name, env[name]]),
  );
}

export function shellQuote(value) {
  return `'${String(value).replaceAll("'", `'"'"'`)}'`;
}

export function claudeCommand(args) {
  if (args.length === 0) throw new Error('Claude arguments are required');
  return ['claude', ...args].map(shellQuote).join(' ');
}

export function writeChunk(stream, data) {
  stream.write(String(data));
}
