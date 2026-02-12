#!/usr/bin/env bash
set -euo pipefail
trap '' PIPE

# List and (optionally) interactively select a Chromium/Chrome profile directory.
#
# Output:
# - default: prints a full path to the chosen profile directory (or empty on failure)
#
# Flags:
# - --list: print available profiles (one per line) and exit
# - --root <path>: only search this Chromium root directory
# - --non-interactive: print instructions and exit non-zero if selection is required
#
# Notes:
# - For Google Chrome on macOS, roots are under:
#   ~/Library/Application Support/Google/Chrome
# - We also consider Chromium, Brave, and Edge roots for convenience.

LIST=0
NON_INTERACTIVE=0
ONLY_ROOT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --list) LIST=1; shift ;;
    --root) ONLY_ROOT="${2:-}"; shift 2 ;;
    --root=*) ONLY_ROOT="${1#*=}"; shift ;;
    --non-interactive) NON_INTERACTIVE=1; shift ;;
    -h|--help)
      echo "Usage: select_chrome_profile.sh [--list] [--root <path>] [--non-interactive]" >&2
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

platform="$(uname -s | tr '[:upper:]' '[:lower:]')"

roots=()
if [[ -n "$ONLY_ROOT" ]]; then
  roots+=("$ONLY_ROOT")
elif [[ "$platform" == "darwin" ]]; then
  roots+=("$HOME/Library/Application Support/Google/Chrome")
  roots+=("$HOME/Library/Application Support/Chromium")
  roots+=("$HOME/Library/Application Support/BraveSoftware/Brave-Browser")
  roots+=("$HOME/Library/Application Support/Microsoft Edge")
elif [[ "$platform" == "linux" ]]; then
  roots+=("$HOME/.config/google-chrome")
  roots+=("$HOME/.config/chromium")
  roots+=("$HOME/.config/BraveSoftware/Brave-Browser")
  roots+=("$HOME/.config/microsoft-edge")
else
  # Windows: best-effort; keep simple for now (users can pass --root).
  :
fi

declare -a entries=()

python3 - <<'PY' "${roots[@]}" > /tmp/birdy_profiles.$$ || true
import json
import os
import sys
from pathlib import Path

roots = [Path(p) for p in sys.argv[1:] if p]
out = []

def read_info_cache(local_state: Path) -> dict:
    try:
        data = json.loads(local_state.read_text(encoding="utf-8", errors="replace"))
    except Exception:
        return {}
    prof = data.get("profile") or {}
    cache = prof.get("info_cache") or {}
    if isinstance(cache, dict):
        return cache
    return {}

for root in roots:
    local_state = root / "Local State"
    if not local_state.exists():
        continue
    cache = read_info_cache(local_state)
    # Profile dirs are typically "Default", "Profile 1", ...
    for child in sorted(root.iterdir()):
        if not child.is_dir():
            continue
        name = child.name
        if name == "Default" or name.startswith("Profile "):
            info = cache.get(name) or {}
            display = info.get("name") or ""
            out.append((str(root), name, display, str(child)))

for root, prof, display, full in out:
    # TSV: root  profile_dir  display_name  full_path
    sys.stdout.write("\t".join([root, prof, display, full]) + "\n")
PY

if [[ -f "/tmp/birdy_profiles.$$" ]]; then
  while IFS=$'\t' read -r root prof display full; do
    [[ -n "$full" ]] || continue
    label="$prof"
    if [[ -n "$display" ]]; then
      label="$display ($prof)"
    fi
    entries+=("$label"$'\t'"$full"$'\t'"$root")
  done < "/tmp/birdy_profiles.$$"
  rm -f "/tmp/birdy_profiles.$$"
fi

if [[ ${#entries[@]} -eq 0 ]]; then
  echo "No Chrome/Chromium profiles found. Pass --root to specify a profile root directory." >&2
  exit 1
fi

if [[ "$LIST" -eq 1 ]]; then
  for e in "${entries[@]}"; do
    IFS=$'\t' read -r label full root <<<"$e"
    # Suppress broken-pipe noise when callers pipe to `head`, etc.
    printf '%s\t%s\t%s\n' "$label" "$full" "$root" 2>/dev/null || exit 0
  done
  exit 0
fi

if [[ "$NON_INTERACTIVE" -eq 1 ]]; then
  echo "Selection required. Run with --list to see available profiles, or run in an interactive terminal." >&2
  exit 1
fi

if [[ ! -t 0 || ! -t 1 ]]; then
  echo "Not a TTY. Run with --list and pass the chosen path via --chrome-profile." >&2
  exit 1
fi

stty_orig="$(stty -g)"
cleanup() {
  stty "$stty_orig" 2>/dev/null || true
  printf '\033[?25h' 2>/dev/null || true
}
trap cleanup EXIT

stty -echo -icanon time 0 min 0
printf '\033[?25l'

idx=0

render() {
  printf '\033[2J\033[H'
  echo "Select Chrome profile (Up/Down, Enter to choose, q to quit)"
  echo
  for i in "${!entries[@]}"; do
    IFS=$'\t' read -r label full root <<<"${entries[$i]}"
    if [[ "$i" -eq "$idx" ]]; then
      printf "> %s\n" "$label"
      printf "  %s\n" "$full"
    else
      printf "  %s\n" "$label"
    fi
  done
}

render

while true; do
  key="$(dd bs=1 count=1 2>/dev/null || true)"
  if [[ -z "$key" ]]; then
    sleep 0.02
    continue
  fi
  if [[ "$key" == $'\n' || "$key" == $'\r' ]]; then
    IFS=$'\t' read -r _label full _root <<<"${entries[$idx]}"
    printf '\033[2J\033[H'
    echo "$full"
    exit 0
  fi
  if [[ "$key" == "q" ]]; then
    exit 1
  fi
  if [[ "$key" == $'\x1b' ]]; then
    k2="$(dd bs=1 count=1 2>/dev/null || true)"
    k3="$(dd bs=1 count=1 2>/dev/null || true)"
    if [[ "$k2" == "[" ]]; then
      if [[ "$k3" == "A" ]]; then
        ((idx--)) || true
      elif [[ "$k3" == "B" ]]; then
        ((idx++)) || true
      fi
      if (( idx < 0 )); then idx=0; fi
      if (( idx >= ${#entries[@]} )); then idx=$((${#entries[@]}-1)); fi
      render
    fi
  fi
done
