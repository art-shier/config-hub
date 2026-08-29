#!/usr/bin/env bash
set -euo pipefail

if ! go_version_output="$(go version 2>/dev/null)" ||
  [[ ! "$go_version_output" =~ (^|[[:space:]])go([0-9]+)\.([0-9]+)(\.[0-9]+)?([[:space:]]|$) ]] ||
  ((10#${BASH_REMATCH[2]} != 1 || 10#${BASH_REMATCH[3]} != 25)); then
  printf '%s\n' 'ConfigHub requires Go 1.25.x' >&2
  exit 1
fi

if ! node_version_output="$(node --version 2>/dev/null)" ||
  [[ ! "$node_version_output" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  printf '%s\n' 'ConfigHub requires Node.js ^22.22.2 || ^24.15.0 || >=26.0.0' >&2
  exit 1
fi
node_major=$((10#${BASH_REMATCH[1]}))
node_minor=$((10#${BASH_REMATCH[2]}))
node_patch=$((10#${BASH_REMATCH[3]}))
if ! ((
  (node_major == 22 && (node_minor > 22 || (node_minor == 22 && node_patch >= 2))) ||
    (node_major == 24 && node_minor >= 15) ||
    node_major >= 26
)); then
  printf '%s\n' 'ConfigHub requires Node.js ^22.22.2 || ^24.15.0 || >=26.0.0' >&2
  exit 1
fi
