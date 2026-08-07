<p align="center">
  <img src="https://raw.githubusercontent.com/guzus/birdy/main/assets/birdy-hero.png" alt="Birdy — Read X faster than light. brew install birdy" width="760">
</p>

Lightweight multi-account X/Twitter CLI. Store several auth tokens and rotate
between them automatically to spread rate-limit pressure. One Go binary — no
Node runtime, no `node_modules`.

```bash
brew tap guzus/tap && brew trust guzus/tap && brew install birdy
# or: curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash
```

All 24 commands are served natively in Go. `pkg/tweet` is the embeddable
library for reading X from a Go service, frozen under semver — see
[COMPATIBILITY.md](https://github.com/guzus/birdy/blob/main/COMPATIBILITY.md)
for what it covers, and what it cannot.
