#!/bin/sh
set -eu

# Set up environment variables for GitHub blob base URL
if [ -n "${INPUT_REPO_BLOB_BASE:-}" ]; then
  export SLINKY_REPO_BLOB_BASE_URL="${INPUT_REPO_BLOB_BASE}"
elif [ -n "${GITHUB_REPOSITORY:-}" ]; then
  COMMIT_SHA="${GITHUB_SHA:-}"
  if [ -n "${GITHUB_EVENT_PATH:-}" ] && command -v jq >/dev/null 2>&1; then
    PR_HEAD_SHA="$(jq -r '.pull_request.head.sha // empty' "$GITHUB_EVENT_PATH" || true)"
    if [ -n "$PR_HEAD_SHA" ]; then
      COMMIT_SHA="$PR_HEAD_SHA"
    fi
  fi
  if [ -n "$COMMIT_SHA" ]; then
    export SLINKY_REPO_BLOB_BASE_URL="https://github.com/${GITHUB_REPOSITORY}/blob/${COMMIT_SHA}"
  fi
fi

# Build command arguments
set -- check

# Add optional flags
if [ -n "${INPUT_CONCURRENCY:-}" ]; then
  set -- "$@" --concurrency "${INPUT_CONCURRENCY}"
fi

if [ -n "${INPUT_TIMEOUT:-}" ]; then
  set -- "$@" --timeout "${INPUT_TIMEOUT}"
fi

if [ -n "${INPUT_JSON_OUT:-}" ]; then
  set -- "$@" --json-out "${INPUT_JSON_OUT}"
fi

if [ -n "${INPUT_MD_OUT:-}" ]; then
  set -- "$@" --md-out "${INPUT_MD_OUT}"
fi

if [ -n "${INPUT_REPO_BLOB_BASE:-}" ]; then
  set -- "$@" --repo-blob-base "${INPUT_REPO_BLOB_BASE}"
fi

if [ "${INPUT_FAIL_ON_FAILURES:-true}" = "true" ]; then
  set -- "$@" --fail-on-failures=true
else
  set -- "$@" --fail-on-failures=false
fi

if [ "${INPUT_COMMENT_PR:-true}" = "true" ]; then
  set -- "$@" --comment-pr=true
else
  set -- "$@" --comment-pr=false
fi

if [ "${INPUT_RESPECT_GITIGNORE:-true}" = "true" ]; then
  set -- "$@" --respect-gitignore=true
else
  set -- "$@" --respect-gitignore=false
fi

# Add targets
if [ -n "${INPUT_TARGETS:-}" ]; then
  # Split comma-separated targets and add each one
  IFS=','
  for target in $INPUT_TARGETS; do
    target=$(echo "$target" | xargs)  # trim whitespace
    if [ -n "$target" ]; then
      set -- "$@" "$target"
    fi
  done
  unset IFS
else
  # Default: scan everything
  set -- "$@" "**/*"
fi

# Debug output
if [ "${ACTIONS_STEP_DEBUG:-}" = "true" ]; then
  printf "::debug:: CLI Args: slinky %s\n" "$*"
fi

# Execute the command
set +e
slinky "$@"
SLINKY_EXIT_CODE=$?
set -e

# Expose outputs
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  if [ -n "${INPUT_JSON_OUT:-}" ]; then
    echo "json_path=${INPUT_JSON_OUT}" >> "$GITHUB_OUTPUT"
  fi
  if [ -n "${INPUT_MD_OUT:-}" ]; then
    echo "md_path=${INPUT_MD_OUT}" >> "$GITHUB_OUTPUT"
  fi
fi

# Append report to job summary if requested
if [ "${INPUT_STEP_SUMMARY:-true}" = "true" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ] && [ -n "${INPUT_MD_OUT:-}" ] && [ -f "${INPUT_MD_OUT}" ]; then
  cat "${INPUT_MD_OUT}" >> "$GITHUB_STEP_SUMMARY"
fi

exit ${SLINKY_EXIT_CODE:-0}