#!/usr/bin/env bash
set -euo pipefail

current_ref="${1:-HEAD}"
previous_ref="${2:-}"
repo_url="${REPO_URL:-https://github.com/guzus/birdy}"

if [[ -z "${previous_ref}" ]]; then
  if [[ "${current_ref}" == "HEAD" ]]; then
    previous_ref="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || true)"
  else
    previous_ref="$(git describe --tags --abbrev=0 "${current_ref}^" 2>/dev/null || true)"
  fi
fi

if [[ -n "${previous_ref}" ]]; then
  range="${previous_ref}..${current_ref}"
else
  range="${current_ref}"
fi

normalize_subject() {
  local subject="$1"
  subject="${subject#feat: }"
  subject="${subject#fix: }"
  subject="${subject#docs: }"
  subject="${subject#style: }"
  subject="${subject#refactor: }"
  subject="${subject#test: }"
  subject="${subject#chore: }"
  subject="${subject#security: }"
  subject="${subject#tui: }"
  if [[ "${subject}" =~ ^[a-z] ]]; then
    subject="${subject^}"
  fi
  printf '%s' "${subject}"
}

classify_subject() {
  local lowered="$1"

  if [[ "${lowered}" =~ ^(docs:|test:|ci:|chore:|style:|refactor:) ]]; then
    printf 'skip'
    return
  fi

  if [[ "${lowered}" =~ skill ]]; then
    printf 'skip'
    return
  fi

  if [[ "${lowered}" =~ smoke\ coverage|smoke\ tests ]]; then
    printf 'skip'
    return
  fi

  if [[ "${lowered}" =~ ^(fix|harden|fail\ fast|avoid|reject|normalize|tighten|validate|respect|route|canonicalize|clear|surface|persist|align|finish|parse|honor|apply|unify|prevent) ]]; then
    printf 'fix'
    return
  fi

  printf 'feature'
}

print_section() {
  local title="$1"
  shift
  local entries=("$@")

  if [[ "${#entries[@]}" -eq 0 ]]; then
    return
  fi

  printf '## %s\n\n' "${title}"
  local entry
  for entry in "${entries[@]}"; do
    printf -- '- %s\n' "${entry}"
  done
  printf '\n'
}

bird_version="$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' third_party/@steipete/bird/package.json | head -n1)"

mapfile -t subjects < <(git log --no-merges --reverse --format='%s' "${range}")

feature_entries=()
fix_entries=()

for subject in "${subjects[@]}"; do
  normalized="$(normalize_subject "${subject}")"
  lowered="$(printf '%s' "${subject}" | tr '[:upper:]' '[:lower:]')"

  case "$(classify_subject "${lowered}")" in
    feature)
      feature_entries+=("${normalized}")
      ;;
    fix)
      fix_entries+=("${normalized}")
      ;;
    skip)
      ;;
  esac
done

release_name="${current_ref}"
if [[ "${current_ref}" == "HEAD" ]]; then
  release_name="Unreleased"
fi

printf '# %s\n\n' "${release_name}"
printf '## Overview\n\n'
if [[ -n "${previous_ref}" ]]; then
  printf -- '- Changes since `%s`\n' "${previous_ref}"
fi
if [[ -n "${bird_version}" ]]; then
  printf -- '- Bundles upstream `bird` CLI `%s`\n' "${bird_version}"
fi
printf -- '- Generated from `git log --no-merges %s`\n\n' "${range}"

print_section "Features" "${feature_entries[@]}"
print_section "Fixes And Hardening" "${fix_entries[@]}"

if [[ -n "${previous_ref}" ]]; then
  printf '**Full Changelog**: %s/compare/%s...%s\n' "${repo_url}" "${previous_ref}" "${current_ref}"
fi
