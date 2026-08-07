#!/usr/bin/env bash
set -euo pipefail

current_ref="${1:-HEAD}"
previous_ref="${2:-}"
repo_url="${REPO_URL:-https://github.com/guzus/birdy}"

if ! git rev-parse -q --verify "${current_ref}^{commit}" >/dev/null; then
  printf 'error: unknown ref %q\n' "${current_ref}" >&2
  exit 1
fi

if [[ -z "${previous_ref}" ]]; then
  if [[ "${current_ref}" == "HEAD" ]]; then
    previous_ref="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || true)"
  else
    previous_ref="$(git describe --tags --abbrev=0 "${current_ref}^" 2>/dev/null || true)"
  fi
fi

if [[ -n "${previous_ref}" ]]; then
  if ! git rev-parse -q --verify "${previous_ref}^{commit}" >/dev/null; then
    printf 'error: unknown previous ref %q\n' "${previous_ref}" >&2
    exit 1
  fi
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


if ! subjects_raw="$(git log --no-merges --reverse --format='%s' "${range}")"; then
  printf 'error: failed to collect commits for range %q\n' "${range}" >&2
  exit 1
fi

subjects=()
if [[ -n "${subjects_raw}" ]]; then
  mapfile -t subjects < <(printf '%s\n' "${subjects_raw}")
fi

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

# A hand-written lede for the release, when there is one. Everything below this
# is generated from git log, which is fine for a changelog and useless for
# saying why a release matters. Edit docs/release-highlight.md before tagging,
# or delete it to fall straight through to the generated sections.
highlight_file="${RELEASE_HIGHLIGHT_FILE:-docs/release-highlight.md}"
if [[ -s "${highlight_file}" ]]; then
  # The highlight is replayed verbatim on every tag, so a version number
  # written into it is wrong on the next release and every one after. v1.0.2
  # shipped announcing "1.0.1" with a section titled "What 1.0.0 commits to",
  # because the file was written for one release and then reused.
  #
  # Anything version-specific belongs in the generated sections below, which
  # are derived from the actual commit range.
  if stray="$(grep -nE '[0-9]+\.[0-9]+\.[0-9]+' "${highlight_file}" | grep -v "${release_name#v}" || true)"; then
    if [[ -n "${stray}" ]]; then
      printf 'error: %s mentions a version number; it is reused for every release\n' "${highlight_file}" >&2
      printf '%s\n' "${stray}" >&2
      exit 1
    fi
  fi

  cat "${highlight_file}"
  printf '\n'
fi

printf '## Overview\n\n'
if [[ -n "${previous_ref}" ]]; then
  printf -- '- Changes since `%s`\n' "${previous_ref}"
fi
printf -- '- Generated from `git log --no-merges %s`\n\n' "${range}"

print_section "Features" "${feature_entries[@]}"
print_section "Fixes And Hardening" "${fix_entries[@]}"

if [[ -n "${previous_ref}" ]]; then
  printf '**Full Changelog**: %s/compare/%s...%s\n' "${repo_url}" "${previous_ref}" "${current_ref}"
fi
