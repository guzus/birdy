# Compatibility policy

birdy follows [Semantic Versioning](https://semver.org/). This document states
exactly what that promise covers, because birdy sits on top of an API that X
does not version and does not document — a blanket "1.0 means stable" claim
would be one birdy cannot keep.

## Covered by semver

A breaking change to any of these requires a major version bump.

| Surface | What is stable |
| --- | --- |
| `pkg/tweet` | Exported identifiers, their signatures, and struct field names, types, and JSON tags |
| CLI commands | Names of commands birdy serves, and their positional arguments |
| CLI flags | Global flags (`--account`, `--strategy`, `--bird`, `--verbose`, `--vpn`, `--vpn-server`) and per-command flags birdy implements |
| Output formats | The shape of `--json` output, and the presence of `--plain` / `--no-emoji` |
| Account store | The `~/.config/birdy/accounts.json` schema and the `BIRDY_ACCOUNTS` JSON shape |
| Exit codes | `0` success, non-zero failure |

Adding a field, command, or flag is a minor bump. Removing or renaming one, or
changing a JSON tag, is a major bump.

`pkg/tweet`'s structs are declared in `pkg/tweet/types.go` as their own types
rather than aliases into `internal/xapi`, precisely so that a parser change
cannot alter this contract without someone deciding to. `TestPublicTypesCoverParserFields`
fails on drift in either direction.

## Not covered by semver

These can change in any release, including a patch.

- **Everything under `internal/`.** Not importable, not stable, no notice given.
- **X's GraphQL API and birdy's behavior against it.** X changes query IDs,
  response shapes, feature flags, and rate limits without warning. birdy tracks
  those changes as fast as it can, and that tracking lands in patch releases.
  A stable `pkg/tweet` signature guarantees your code keeps *compiling*; it
  cannot guarantee X keeps answering. Treat every call as fallible and expect
  `Read`/`Thread` to return errors that did not occur yesterday.
- **Commands forwarded to `bird`.** Their flags, output, and semantics belong to
  [steipete/bird](https://github.com/steipete/bird) upstream, not to birdy. See
  the section below for what *is* promised about them.
- **Pinned third-party versions.** The Bird Box image pins exact versions
  (`@anthropic-ai/claude-code`, `@steipete/bird`, `undici`). These are bumped in
  minor and patch releases.
- **Bird Box, `birdy host`, and Birdy Web.** The hosted surface, its HTTP routes
  (`/api/chat`, SSE event names), the React UI, and the invite flow are
  pre-1.0 and evolve independently of the CLI's version.
- **The TUI's layout, keybindings, and chat transcript format.**
- **Rate-limit and rotation heuristics.** Which account the quota-aware strategy
  picks is an implementation detail; the strategy *names* are covered.

## The bird passthrough, and removing it

birdy is [porting commands from `bird` to native Go](README.md#native-go-engine).
The goal is a CLI with no Node.js dependency and no bundled `node_modules`.

This is deliberately structured so it does not require a major bump:

- **Command names survive the port.** A command that moves from bird-forwarded
  to native keeps its name and positional arguments. `birdy read <id>` works the
  same before and after.
- **Native output is byte-identical to bird's** for `--json`, `--plain` and
  `--no-emoji`, and is verified by diffing both engines against live X.
- **Unimplemented flags fall back rather than lie.** A command carrying a flag
  the native path lacks (`--all`, `--max-pages`, `--cursor`, …) runs through
  bird instead of silently ignoring it.
- **`--bird` / `BIRDY_USE_BIRD=1` is the escape hatch** while both engines exist.

What *will* eventually be a breaking change, and will wait for a major bump:
removing the `--bird` flag and the bundled bird runtime once every command is
native. Until then, a bird-forwarded command's behavior can change whenever
upstream bird changes.

If a command cannot be ported with identical behavior, it will be documented
here rather than quietly diverging. The current list:

- **`query-ids`** reports birdy's persisted-query resolver, not bird's. The two
  keep separate caches with separate override mechanisms, so bird's output
  would describe a file birdy never reads. birdy's adds a `source` field naming
  why each hash was chosen (`generated`, `discovered`, or the
  `BIRDY_<OPERATION>_QUERY_ID` variable that overrode it).
- **`whoami`** resolves the account id from `verify_credentials.json`'s
  top-level `id_str`. bird does not read that field and falls through to
  scraping the HTML settings page; birdy reads what X sends and does not
  implement the scrape. The printed output is the same.
- **`--media`** is not implemented on `tweet`/`reply`. Those invocations fall
  back to bird rather than silently dropping the attachment.

## Reporting a break

If a release breaks one of the covered surfaces without a major bump, that is a
bug — please [open an issue](https://github.com/guzus/birdy/issues).
