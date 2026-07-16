import {
  claudeCommand,
  forwardedEnvs,
  readConfig,
  writeChunk,
} from './config.mjs';

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

export const signalExitCodes = { SIGHUP: 129, SIGINT: 130, SIGTERM: 143 };

// createBirdBoxLifecycle owns exactly one sandbox lifecycle. All external
// effects are injected so lifecycle, streaming, and cleanup behavior can be
// tested without contacting E2B.
export function createBirdBoxLifecycle({
  Sandbox,
  argv,
  env,
  stdout,
  stderr,
  readConfigFn = readConfig,
  commandFn = claudeCommand,
  forwardedEnvsFn = forwardedEnvs,
  writeChunkFn = writeChunk,
}) {
  let config;
  let sandbox;
  let cleanupPromise;
  let shutdownRequested = false;
  let creationSettled = false;
  let resolveCreation;
  const creationPromise = new Promise(resolve => { resolveCreation = resolve; });

  const writeError = message => writeChunkFn(stderr, `${message}\n`);

  function markCreationSettled() {
    if (!creationSettled) {
      creationSettled = true;
      resolveCreation();
    }
  }

  // Wait for in-flight creation before cleanup. Otherwise SIGTERM could let
  // the local runner exit while E2B finishes creating an orphaned sandbox.
  function cleanup() {
    if (!cleanupPromise) {
      cleanupPromise = (async () => {
        await creationPromise;
        if (!sandbox) return 0;
        try {
          await sandbox.kill({ requestTimeoutMs: config.cleanupTimeoutMs });
          return 0;
        } catch (error) {
          writeError(`failed to kill E2B sandbox: ${errorMessage(error)}`);
          return 1;
        }
      })();
    }
    return cleanupPromise;
  }

  async function run() {
    let status = 0;
    try {
      // Configuration and command construction can fail on user-controlled
      // values, so keep both inside the same error boundary as E2B calls.
      config = readConfigFn(env);
      const command = commandFn(argv);
      try {
        sandbox = await Sandbox.create(config.template, {
          apiKey: env.E2B_API_KEY,
          metadata: { application: 'birdy-web', runtime: 'claude-code' },
          requestTimeoutMs: config.requestTimeoutMs,
          secure: true,
          timeoutMs: config.sandboxTimeoutMs,
        });
      } finally {
        markCreationSettled();
      }

      if (!shutdownRequested) {
        const result = await sandbox.commands.run(command, {
          envs: forwardedEnvsFn(env),
          onStdout: data => writeChunkFn(stdout, data),
          onStderr: data => writeChunkFn(stderr, data),
          requestTimeoutMs: config.requestTimeoutMs,
          timeoutMs: config.commandTimeoutMs,
        });
        if (result.exitCode !== 0) {
          throw new Error(`Claude exited with status ${result.exitCode}`);
        }
      }
    } catch (error) {
      markCreationSettled();
      writeError(`E2B Claude execution failed: ${errorMessage(error)}`);
      status = 1;
    } finally {
      markCreationSettled();
      if (await cleanup() !== 0) status = 1;
    }
    return status;
  }

  async function shutdown() {
    shutdownRequested = true;
    return cleanup();
  }

  return { run, shutdown };
}

export function installSignalHandlers(processLike, lifecycle) {
  let handlingSignal = false;
  for (const [signal, exitCode] of Object.entries(signalExitCodes)) {
    processLike.once(signal, () => {
      if (handlingSignal) return;
      handlingSignal = true;
      void lifecycle.shutdown().then(status => {
        processLike.exit(status === 0 ? exitCode : 1);
      }).catch(() => processLike.exit(1));
    });
  }
  return () => handlingSignal;
}
