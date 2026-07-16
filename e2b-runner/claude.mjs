import { Sandbox } from 'e2b';
import { createBirdBoxLifecycle, installSignalHandlers } from './lifecycle.mjs';

const lifecycle = createBirdBoxLifecycle({
  Sandbox,
  argv: process.argv.slice(2),
  env: process.env,
  stdout: process.stdout,
  stderr: process.stderr,
});

const isHandlingSignal = installSignalHandlers(process, lifecycle);

const status = await lifecycle.run();
if (!isHandlingSignal()) process.exitCode = status;
