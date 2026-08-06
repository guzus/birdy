#!/usr/bin/env bash
#
# Benchmarks birdy's native Go read path against the bird (Node) CLI it replaced.
#
#   ./scripts/bench-go-vs-bird.sh
#
# What this measures, and why it is shaped this way:
#
# Both paths ultimately wait on X's API, which takes ~1.5s and varies by more
# than the two implementations differ from each other. Benchmarking against the
# live API would report network weather, not code, so the comparable numbers
# here are the work each client does that is NOT waiting on X:
#
#   - Go:   the full client path (build query -> HTTP -> decode) against a
#           local httptest server, via `go test -bench`.
#   - bird: process spawn + module load + argument parse, measured by running
#           `bird read` with no arguments so it exits before any network call.
#
# That is the per-call cost each pays before and after the network, and it is
# the honest axis of comparison. End-to-end latency against live X is reported
# separately by --live, and is expected to be a wash.
#
# Nothing here consumes X rate-limit budget unless --live is passed.

set -euo pipefail

cd "$(dirname "$0")/.."

BIRD_PATH="${BIRDY_BIRD_PATH:-third_party/@steipete/bird/dist/cli.js}"
RUNS="${RUNS:-8}"
LIVE=0
[[ "${1:-}" == "--live" ]] && LIVE=1

if [[ ! -f "$BIRD_PATH" ]]; then
  echo "bird CLI not found at $BIRD_PATH" >&2
  echo "set BIRDY_BIRD_PATH, or run: npm install -g @steipete/bird" >&2
  exit 1
fi

command -v node >/dev/null || { echo "node is required to benchmark the bird path" >&2; exit 1; }

echo "== environment =="
echo "go:   $(go version | awk '{print $3, $4}')"
echo "node: $(node --version)"
echo

echo "== Go: native client, hermetic (local server, no X) =="
go test ./internal/xapi/ -bench='BenchmarkConversation|BenchmarkParseConversation' \
  -benchtime=2s -run=NONE 2>/dev/null | grep -E '^Benchmark'
echo

echo "== bird: per-call overhead, no network ($RUNS runs) =="
# `read` with no argument loads the full module graph, then fails on argument
# validation before opening a socket.
total=0
for _ in $(seq "$RUNS"); do
  start=$(python3 -c 'import time;print(time.time())')
  node "$BIRD_PATH" read >/dev/null 2>&1 || true
  ms=$(python3 -c "import time;print((time.time()-$start)*1000)")
  total=$(python3 -c "print($total + $ms)")
done
python3 -c "print('  mean: %.1f ms/call' % ($total / $RUNS))"
echo

echo "== concurrency: wall time to complete N calls (overhead only) =="
printf '  %-6s %-22s %s\n' "N" "bird (Node)" "note"
for n in 1 8 32; do
  start=$(python3 -c 'import time;print(time.time())')
  for _ in $(seq "$n"); do node "$BIRD_PATH" read >/dev/null 2>&1 & done
  wait
  ms=$(python3 -c "import time;print((time.time()-$start)*1000)")
  printf '  %-6s %-22s %s\n' "$n" \
    "$(python3 -c "print('%.0f ms (%.1f ms/op)' % ($ms, $ms/$n))")" \
    "$(python3 -c "print('~%d MB resident' % ($n * 60))")"
done
echo
echo "  Go, same shape (in-process, pooled connections):"
go test ./internal/xapi/ -bench='BenchmarkConversationParallel' \
  -benchtime=2s -run=NONE -cpu=1,8 2>/dev/null | grep -E '^Benchmark' | sed 's/^/    /'
echo

echo "== peak RSS, one bird process =="
if /usr/bin/time -l true >/dev/null 2>&1; then
  /usr/bin/time -l node "$BIRD_PATH" read 2>&1 | awk '/maximum resident/ {printf "  %.0f MB\n", $1/1048576}'
else
  /usr/bin/time -v node "$BIRD_PATH" read 2>&1 | awk '/Maximum resident/ {printf "  %.0f MB\n", $6/1024}'
fi

if [[ "$LIVE" == "1" ]]; then
  echo
  echo "== end-to-end against live X (consumes rate-limit budget) =="
  echo "   expect parity: X's latency dominates both paths"
  go test ./pkg/tweet/ -run TestIntegration -v 2>&1 | grep -E '^(---|ok)' || true
fi
