#!/usr/bin/env bash
#
# Differential test: run every command through both engines against live X and
# diff the output.
#
# This exists because mocks are not an oracle. birdy's native `whoami` passed a
# full httptest suite and failed on the first live call: every v1.1 account
# endpoint X once served now answers 404, which no fixture written from bird's
# source could have revealed. bird, running against the same live API in the
# same binary, catches that in one command.
#
# That is the whole method. bird is the specification; this script is how the
# specification gets consulted. Until a command's output matches here, it is
# not ported — a green `go test` only says the code does what its author
# thought bird does.
#
# Usage:
#   scripts/diff-engines.sh                  # read-only commands, all modes
#   scripts/diff-engines.sh --account alt3   # pin an account
#   scripts/diff-engines.sh --quick          # one mode, fewer commands
#
# Reads only. Nothing here posts, follows, or unbookmarks; the write commands
# have no safe differential test, which is exactly why they stay behind a
# manual burner-account check.

set -uo pipefail

BIRDY="${BIRDY:-./birdy}"
ACCOUNT=""
QUICK=0
OUT="${OUT:-/tmp/birdy-diff-$(date +%Y%m%d-%H%M%S)}"

while [ $# -gt 0 ]; do
  case "$1" in
    --account) ACCOUNT="--account $2"; shift 2 ;;
    --quick)   QUICK=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [ ! -x "$BIRDY" ]; then
  echo "no birdy binary at $BIRDY (run: go build -o birdy .)" >&2
  exit 2
fi

# Without a pinned account each invocation takes the next slot in the rotation,
# so the two engines get compared as two different users and every personalised
# command fails for a reason unrelated to the port.
if [ -z "$ACCOUNT" ]; then
  first=$("$BIRDY" account list 2>/dev/null | awk 'NR==2{print $1}')
  if [ -z "$first" ]; then
    echo "no accounts configured; add one or pass --account" >&2
    exit 2
  fi
  ACCOUNT="--account $first"
  echo "pinning both engines to account: $first"
fi

mkdir -p "$OUT"

# A tweet and a handle that will still exist tomorrow. Override when they rot;
# a deleted fixture shows up as both engines erroring identically, which passes
# and proves nothing.
TWEET_ID="${BIRDY_DIFF_TWEET_ID:-2085594813840216212}"
HANDLE="${BIRDY_DIFF_HANDLE:-@steipete}"

# Commands whose result is stable between two calls seconds apart. Only these
# can be compared byte-for-byte.
# activity is NOT here: its nextCursor embeds a timestamp, so two calls seconds
# apart differ on every run.
DETERMINISTIC="read thread replies about whoami check user-tweets"

# Everything else is a live, ranked, or personalised feed: home returns
# different tweets on consecutive calls, search re-ranks, bookmarks and likes
# move. Diffing their content compares X against itself and fails for reasons
# that have nothing to do with the port. They are compared structurally
# instead — same field names, same line shapes — which is what actually
# regresses when a parser changes.
COMMANDS=(
  "read $TWEET_ID"
  "thread $TWEET_ID"
  "replies $TWEET_ID"
  "search golang -n 5"
  "user-tweets $HANDLE -n 5"
  "home -n 5"
  "bookmarks -n 5"
  "likes -n 5"
  "whoami"
  "about $HANDLE"
  "followers -n 5"
  "following -n 5"
  "lists"
  "mentions -n 5"
  "activity $TWEET_ID"
  "check"
)

if [ "$QUICK" = 1 ]; then
  MODES=("--plain")
  COMMANDS=("read $TWEET_ID" "search golang -n 5" "whoami" "home -n 5")
else
  # Each mode has its own separators, labels and emoji, and ARA parses more
  # than one of them.
  MODES=("--plain" "--json --plain" "")
fi

# shape reduces output to what should be stable regardless of which tweets came
# back: the set of JSON field paths, or the sequence of line kinds.
shape() {
  local file="$1" mode="$2"
  if [[ "$mode" == *--json* ]]; then
    python3 - "$file" <<'PYEOF'
import json, sys
def paths(node, prefix=""):
    if isinstance(node, dict):
        for k in sorted(node):
            yield from paths(node[k], f"{prefix}.{k}")
    elif isinstance(node, list):
        # Every element, not just the first: a field only some entries carry
        # (quotedTweet, media) is invisible if element 0 lacks it.
        yield f"{prefix}[]:len={len(node)}"
        for item in node:
            yield from paths(item, f"{prefix}[]")
    else:
        yield f"{prefix}:{type(node).__name__}"
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(0)
for p in sorted(set(paths(data))):
    print(p)
PYEOF
  else
    # Collapse each line to its kind: the label or prefix, not the payload.
    sed -E \
      -e 's/^@[A-Za-z0-9_]+ \(.*\):$/HANDLE_LINE/' \
      -e 's#^(date|url|source|user|user_id|engine|credentials):.*#\1:#' \
      -e 's#^(📅|🔗|📍|🙋|🪪|⚙️|🔑|ℹ️|✅|❌) .*#\1#' \
      -e 's#^(PHOTO|VIDEO|GIF):.*#\1:#' \
      -e 's#^(🖼️|🎬|🔄) .*#\1#' \
      -e 's#^likes: [0-9]+.*#STATS#' \
      -e 's#^❤️ .*#STATS#' \
      -e 's#^.+$#TEXT#' "$file" | grep -v '^$' | uniq
    # Entry count is part of the shape: a parser that drops or adds whole
    # records leaves the per-line kinds identical.
    printf 'ENTRIES=%s\n' "$(grep -c '──────────' "$file" 2>/dev/null || echo 0)"
  fi
}

pass=0; fail=0; skip=0
FAILED=()

for cmd in "${COMMANDS[@]}"; do
  for mode in "${MODES[@]}"; do
    label="$cmd ${mode:-<emoji>}"
    slug=$(echo "$label" | tr -c 'a-zA-Z0-9' '-')

    # shellcheck disable=SC2086
    "$BIRDY" $ACCOUNT $cmd $mode >"$OUT/$slug.native" 2>"$OUT/$slug.native.err"
    native_rc=$?
    # shellcheck disable=SC2086
    "$BIRDY" $ACCOUNT --bird $cmd $mode >"$OUT/$slug.bird" 2>"$OUT/$slug.bird.err"
    bird_rc=$?

    # A rate limit makes both engines return nothing, which diffs clean and
    # proves nothing. Treat it as skipped, never as passed.
    if grep -qi "rate limit\|HTTP 429" "$OUT/$slug.native.err" "$OUT/$slug.bird.err" 2>/dev/null; then
      echo "SKIP  $label  (rate limited)"
      skip=$((skip+1))
      continue
    fi

    # A pass requires an actual answer. Two identical failures — a deleted
    # fixture, a dead endpoint — diff clean and prove nothing.
    if [ "$native_rc" -ne 0 ] || [ "$bird_rc" -ne 0 ]; then
      echo "FAIL  $label  (native rc=$native_rc, bird rc=$bird_rc; neither may fail)"
      head -2 "$OUT/$slug.native.err" "$OUT/$slug.bird.err" 2>/dev/null | sed 's/^/        /'
      FAILED+=("$label")
      fail=$((fail+1))
      continue
    fi
    if [ ! -s "$OUT/$slug.native" ] || [ ! -s "$OUT/$slug.bird" ]; then
      echo "FAIL  $label  (empty output from one or both engines)"
      FAILED+=("$label")
      fail=$((fail+1))
      continue
    fi

    if [ "$native_rc" -ne "$bird_rc" ]; then
      echo "FAIL  $label  (exit $native_rc vs $bird_rc)"
      FAILED+=("$label")
      fail=$((fail+1))
      continue
    fi

    # shellcheck disable=SC2086
    engine=$("$BIRDY" $ACCOUNT -v $cmd $mode 2>&1 >/dev/null | sed -n 's/.*engine: //p' | head -1)
    if [ "$engine" != "native (go)" ]; then
      echo "SKIP  $label  (served by ${engine:-unknown}, not native — nothing to compare)"
      skip=$((skip+1))
      sleep "${BIRDY_DIFF_DELAY:-2}"
      continue
    fi

    verb="${cmd%% *}"
    if [[ " $DETERMINISTIC " == *" $verb "* ]]; then
      if diff -q "$OUT/$slug.native" "$OUT/$slug.bird" >/dev/null 2>&1; then
        echo "ok    $label"
        pass=$((pass+1))
      else
        echo "FAIL  $label"
        diff "$OUT/$slug.native" "$OUT/$slug.bird" | head -15 | sed 's/^/        /'
        FAILED+=("$label")
        fail=$((fail+1))
      fi
    else
      shape "$OUT/$slug.native" "$mode" >"$OUT/$slug.native.shape"
      shape "$OUT/$slug.bird" "$mode" >"$OUT/$slug.bird.shape"
      if diff -q "$OUT/$slug.native.shape" "$OUT/$slug.bird.shape" >/dev/null 2>&1; then
        echo "ok    $label  (shape; content is live)"
        pass=$((pass+1))
      else
        echo "FAIL  $label  (shape differs)"
        diff "$OUT/$slug.native.shape" "$OUT/$slug.bird.shape" | head -15 | sed 's/^/        /'
        FAILED+=("$label")
        fail=$((fail+1))
      fi
    fi

    sleep "${BIRDY_DIFF_DELAY:-2}"
  done
done

echo
echo "pass=$pass fail=$fail skip=$skip   artifacts: $OUT"

if [ "$skip" -gt 0 ]; then
  echo "NOTE: $skip case(s) skipped, not verified. A skip is not a pass."
fi

if [ "$fail" -gt 0 ]; then
  printf 'failed:\n'
  printf '  %s\n' "${FAILED[@]}"
  exit 1
fi
