import { Sandbox } from 'e2b';

import {
  claudeCommand,
  forwardedEnvs,
  readConfig,
  writeChunk,
} from './config.mjs';

const config = readConfig();
const command = claudeCommand(process.argv.slice(2));
let sandbox;
let cleanupPromise;

function cleanup() {
  if (!cleanupPromise) {
    cleanupPromise = sandbox?.kill({ requestTimeoutMs: config.cleanupTimeoutMs }) ?? Promise.resolve();
  }
  return cleanupPromise;
}

const signalExitCodes = { SIGHUP: 129, SIGINT: 130, SIGTERM: 143 };
for (const signal of Object.keys(signalExitCodes)) {
  process.once(signal, () => {
    void cleanup()
      .catch(error => console.error(`failed to kill E2B sandbox: ${error.message}`))
      .finally(() => process.exit(signalExitCodes[signal]));
  });
}

try {
  sandbox = await Sandbox.create(config.template, {
    apiKey: process.env.E2B_API_KEY,
    metadata: { application: 'birdy-web', runtime: 'claude-code' },
    requestTimeoutMs: config.requestTimeoutMs,
    secure: true,
    timeoutMs: config.sandboxTimeoutMs,
  });

  const result = await sandbox.commands.run(command, {
    envs: forwardedEnvs(),
    // Envd already supplies the command's stream chunks. Do not reframe or
    // re-encode them: Claude's JSONL must reach birdy's parser byte-for-byte.
    onStdout: data => writeChunk(process.stdout, data),
    onStderr: data => writeChunk(process.stderr, data),
    requestTimeoutMs: config.requestTimeoutMs,
    timeoutMs: config.commandTimeoutMs,
  });

  if (result.exitCode !== 0) {
    throw new Error(`Claude exited with status ${result.exitCode}`);
  }
} catch (error) {
  console.error(`E2B Claude execution failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
} finally {
  try {
    await cleanup();
  } catch (error) {
    console.error(`failed to kill E2B sandbox: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  }
}
