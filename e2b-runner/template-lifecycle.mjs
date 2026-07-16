export async function buildSmokeAndPromote({
  Sandbox,
  Template,
  buildOptions,
  candidateTag,
  name,
  promotionTag,
  smokeCommand,
  template,
}) {
  const build = await Template.build(template, `${name}:${candidateTag}`, buildOptions);
  const immutableTarget = `${name}:${build.buildId}`;
  let sandbox;
  try {
    sandbox = await Sandbox.create(immutableTarget, {
      metadata: { application: 'birdy-template-build', runtime: 'smoke-test' },
      requestTimeoutMs: 30_000,
      secure: true,
      timeoutMs: 120_000,
    });
    const smoke = await sandbox.commands.run(smokeCommand, {
      requestTimeoutMs: 30_000,
      timeoutMs: 60_000,
    });
    if (smoke.exitCode !== 0) {
      throw new Error(`Bird Box smoke test exited with status ${smoke.exitCode}`);
    }
  } finally {
    if (sandbox) await sandbox.kill({ requestTimeoutMs: 30_000 });
  }

  await Template.assignTags(immutableTarget, promotionTag);
  return { immutableTarget, promotionTarget: `${name}:${promotionTag}` };
}
