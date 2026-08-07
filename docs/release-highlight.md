<p align="center">
  <img src="https://raw.githubusercontent.com/guzus/birdy/main/assets/birdy-dogfood.png" alt="Dogfooded in production by ai-research-arm: 9 divergences found, 0 caught by the test suite, 3 agents running read-only against live X" width="820">
</p>

## Dogfooded in production by ai-research-arm

birdy is replacing the Node `bird` CLI with a native Go engine, command by command.
This release is the first that [ai-research-arm](https://github.com/guzus/ai-research-arm)
— birdy's heaviest consumer, whose hourly pipeline pulls X through it — put through a
full differential run against live X.

Because both engines can run side by side, every command can be answered twice and
compared:

```bash
birdy read <id> --json        # native Go
birdy --bird read <id> --json # the Node reference
scripts/diff-engines.sh       # every read command, every output mode
```

**It found nine output divergences. The test suite had caught none of them.** That is the
point of the exercise, not an embarrassment to bury: mocks verify that the code does what
its author believed `bird` does, and only `bird` verifies what `bird` does. Among them:

- **Quoted tweets were dropped entirely** — roughly a third of a live timeline quotes
  another tweet, and every one of those was rendering as a bare comment with the thing it
  referred to missing.
- **`followers` returned nothing on every account.** X's GraphQL `Followers` operation
  404s for cookie sessions; the v1.1 fallback that `bird` carries had not been ported.
- **`whoami` could not resolve the account at all**, for the same reason — the endpoints
  it used are gone.
- Smaller ones with the same shape: `search` defaulted to 20 results where `bird` uses 10,
  `--latest` was accepted and silently ignored on seven commands, and `-n 0` returned a
  full page instead of an error.

The differential harness itself was also wrong, and ai-research-arm caught that too: its
default fixture tweet had been deleted, so four commands reported `ok` while both engines
were failing identically. A passing comparison now requires a real answer from both sides.

**This release does not claim parity.** Several divergences are still open and tracked in
[`COMPATIBILITY.md`](https://github.com/guzus/birdy/blob/main/COMPATIBILITY.md).

`--bird` is also why this release can drop Node without losing the ability to check itself.
The archive is now a single Go binary — no bundled `bird`, no `node_modules` — but the flag
survives, resolving `bird` from your `PATH` when you install it separately. Users get a
Node-free binary; anyone who wants to re-run the comparison above still can. The method, and
why that escape hatch outlives the migration, is in
[`docs/MIGRATION.md`](https://github.com/guzus/birdy/blob/main/docs/MIGRATION.md).
