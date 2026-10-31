#!/usr/bin/env bash
# PostToolUse hook: gofmt any .go file Claude just edited.
# Saves a round-trip (and tokens) vs. Claude running gofmt itself.
set -euo pipefail
command -v jq >/dev/null 2>&1 || exit 0
command -v gofmt >/dev/null 2>&1 || exit 0
file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go && -f "$file" ]] || exit 0
gofmt -w "$file"
