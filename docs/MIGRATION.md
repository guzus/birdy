# Migrating birdy off Node

How birdy replaces the Node `bird` CLI with a native Go engine without breaking
the things that depend on it.

## The lesson that shaped this

birdy's native `whoami` passed a complete `httptest` suite — request shape,
response parsing, error paths, caching, rate-limit short-circuit — and failed
on its first call against live X. Every v1.1 account endpoint it targeted now
answers `404 Sorry, that page does not exist`. No fixture could have shown
that, because the fixtures were written from the same source that produced the
bug: a reading of bird's code.

Worse, the reasoning that introduced it was locally sound. bird carries an HTML
settings-page scrape after those endpoints, and it looked like a workaround for
bird failing to read `verify_credentials`' top-level `id_str`. Reading the
field directly did produce identical output — against a mock. Against X the
endpoints are simply gone, and the scrape is the only thing that works.

One `--bird` invocation would have caught it in a second.

So: **mocks verify that the code does what its author believed bird does. Only
bird verifies what bird does.** Everything below follows from that.

## What we take from Bun's Zig → Rust rewrite

[Bun rewrote 1M+ lines](https://bun.com/blog/bun-in-rust) and validated it
against a test suite written in TypeScript — a suite that could not tell which
language the runtime was written in, and therefore could not be tuned to agree
with the new implementation. That language-independent oracle was the thing
that made a wholesale rewrite safe. Their other rules follow from having it:
compiler errors as the work queue, 1 implementer to 2+ adversarial reviewers on
split context windows, zero tests skipped or deleted, merge only at 100%.

Two things differ here, and both matter.

**birdy's oracle is better.** Bun's suite is a fixed description of intended
behavior. birdy's oracle is bird itself, running against the same live API, in
the same binary, right now — so it catches X-side drift that no recorded
expectation can. `scripts/diff-engines.sh` is how it is consulted.

**birdy cannot do a wholesale cutover.** Bun replaced a runtime whose semantics
it owned. birdy sits on an undocumented API that changes without notice, so a
big-bang switch would take every command down at once when one query hash
rotates. The port goes command by command, with both engines shipping together
and `--bird` as the escape hatch, which is also what keeps the oracle available.

What we keep unchanged from Bun: **a skip is not a pass.** The harness counts
skips separately and says so, because a rate-limited run produces empty output
from both engines and diffs clean.

## The oracle

```bash
go build -o birdy .
scripts/diff-engines.sh --account <name>     # all commands, all output modes
scripts/diff-engines.sh --quick              # smoke
```

It runs every read command through both engines and compares. Two comparison
modes, because not every command is deterministic:

| Kind | Commands | Compared by |
| --- | --- | --- |
| Deterministic | `read` `thread` `replies` `about` `whoami` `check` `activity` `user-tweets` | exact bytes |
| Live / ranked | `home` `search` `bookmarks` `likes` `followers` `following` `lists` `mentions` | structure — JSON field paths, or the sequence of line kinds |

The second row exists because `home` returns different tweets on consecutive
calls. Diffing its content compares X against itself and fails for reasons that
have nothing to do with the port; diffing its *shape* still catches the thing
that actually regresses, which is a parser emitting different fields.

Every output mode is checked (`--plain`, `--json --plain`, emoji), because each
has its own separators, labels and truncation rules, and consumers parse more
than one of them.

## Definition of done, per command

A command is ported when all of these hold. Not when its unit tests pass.

1. `go test ./...` passes, including a test that fails if the parser drifts.
2. `scripts/diff-engines.sh` shows it matching bird in every output mode.
3. Flags the native path does not implement fall back to bird rather than being
   ignored — verified by a test, not by inspection.
4. Any deliberate divergence is written down in
   [`COMPATIBILITY.md`](../COMPATIBILITY.md), with the reason.
5. A consumer that actually parses the output has run against it. For birdy
   that is [ai-research-arm](https://github.com/guzus/ai-research-arm), whose
   `.claude/agents/birdy-dogfood.md` replays its real invocations.

## The write commands are a hole in this

`tweet`, `reply`, `follow`, `unfollow` and `unbookmark` have **no safe
differential test**. Running the oracle on them would post twice.

They are therefore the one place where the method above does not apply, and
they get the treatment that gap deserves:

- verified by hand against a dedicated burner account, never by the harness;
- no retries anywhere in the path, because a timed-out `CreateTweet` may have
  posted and a duplicate is the worse failure;
- bird's further `statuses/update` fallback deliberately not ported, for the
  same reason;
- `--bird` stays available for them longest.

Treat "the write tests pass" as meaning only that birdy sends what bird's
source says to send.

## Order of work

Bird's removal is sequenced so no step is a breaking change until the last one,
which is why it can land before 1.0.0 rather than waiting for 2.0.

1. **Port the commands.** 23 of 24 done; `news` remains.
2. **Native VPN routing.** The `undici` dependency exists only to push bird's
   Node `fetch` through the SOCKS5 bridge; Go dials SOCKS5 directly. This
   deletes `bootstrap.js`, `birdy vpn install-deps`, and the local HTTP CONNECT
   bridge.
3. **Drop the bundle.** Remove `third_party/@steipete/bird` from
   `.goreleaser.yaml`, `install.sh`, and the Dockerfile's global npm install.
   The release stops shipping `node_modules`.
4. **Remove `--bird` / `BIRDY_USE_BIRD`.** This is the only breaking step, and
   it also removes the oracle — so it goes last, after a release where every
   command has been differentially verified in the field.

Step 4 is worth resisting for a while. As long as bird ships alongside, any
report of "birdy returns the wrong thing" can be answered in one command.

## Known divergences

Recorded in [`COMPATIBILITY.md`](../COMPATIBILITY.md), not here, so there is
one list rather than two. Currently: `query-ids` reports birdy's resolver
rather than bird's cache, and `--media` falls back rather than being dropped.
