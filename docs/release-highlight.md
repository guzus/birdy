<p align="center">
  <img src="https://raw.githubusercontent.com/guzus/birdy/main/assets/birdy-hero.png" alt="Birdy — Read X faster than light. brew install birdy" width="820">
</p>

## 1.0.1 — one Go binary, no Node

birdy began as a multi-account proxy in front of the Node
[bird](https://github.com/steipete/bird) CLI. It now talks to X natively in Go.
All 24 commands, reads and writes, are served in-process.

**The release is a single binary.** No bundled `bird`, no `node_modules`, no
Node runtime. `install.sh` unpacks one file.

```bash
brew tap guzus/tap && brew trust guzus/tap && brew install birdy
# or: curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash
birdy account add main && birdy tui
```

### What 1.0.0 commits to

`pkg/tweet` is birdy's embeddable Go API — read X from a Go service without
shelling out or standing up a host. Its exported names, struct fields, JSON tags
and **field order** are frozen under semver, along with command and flag names,
`--json` output shape, and the account store schema.
[`COMPATIBILITY.md`](https://github.com/guzus/birdy/blob/main/COMPATIBILITY.md)
states exactly what that covers — and, more usefully, what it cannot: X's
GraphQL API is unversioned and undocumented, so a stable signature guarantees
your code keeps compiling, not that a call keeps succeeding.

### How it was verified

Not by the test suite. Both engines ship together, so every command can be
answered twice and diffed against live X:

```bash
scripts/diff-engines.sh          # 16 commands x 3 output modes, both engines
```

The full matrix passes **47 of 48**, with no skips — a skipped case is not a
passing one. The single remaining difference is documented: birdy returns
*more* per-user data than bird on `following`/`followers`, because it sends the
persisted-query hash its variables were vetted against while bird discovers a
newer one that returns less.

That method found nine output divergences the entire Go test suite had missed,
when [ai-research-arm](https://github.com/guzus/ai-research-arm) — birdy's
heaviest consumer — replayed its production workloads through both engines.
Quoted tweets were being dropped from roughly a third of a timeline.
`followers` returned nothing at all. `whoami` could not resolve the account.
Every one of those passed CI.

So `--bird` survives 1.0.0. It resolves bird from your `PATH` when you install
it separately, and it is the instrument that keeps this checkable. The
reasoning is in
[`docs/MIGRATION.md`](https://github.com/guzus/birdy/blob/main/docs/MIGRATION.md).

### Also in this release

- `birdy account disable <name>` takes an account out of rotation without
  discarding its credentials — for a rate-limited, suspended, or stale login.
- `--vpn` routes the native engine through SOCKS5 directly, with no local
  bridge and no `undici`. It had silently become a no-op as commands were
  ported; it is now verified by a measured change of egress IP.
- Timeline commands page to fill `-n` instead of silently truncating at one
  page. This costs up to `ceil(n/20)` requests where it previously cost one.
