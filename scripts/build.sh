#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"

cd "$repo_root"

if ! go_version_output="$(go version 2>/dev/null)" ||
  [[ ! "$go_version_output" =~ (^|[[:space:]])go([0-9]+)\.([0-9]+)(\.[0-9]+)?([[:space:]]|$) ]] ||
  ((10#${BASH_REMATCH[2]} != 1 || 10#${BASH_REMATCH[3]} != 25)); then
  printf '%s\n' 'build requires Go 1.25.x' >&2
  exit 1
fi

if ! node_version_output="$(node --version 2>/dev/null)" ||
  [[ ! "$node_version_output" =~ ^v?([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  printf '%s\n' 'build requires Node.js ^22.22.2 || ^24.15.0 || >=26.0.0' >&2
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
  printf '%s\n' 'build requires Node.js ^22.22.2 || ^24.15.0 || >=26.0.0' >&2
  exit 1
fi

raw_version="$(git -C "$repo_root" describe --always --dirty)"
build_version="$(printf '%s' "$raw_version" | LC_ALL=C sed 's/[^A-Za-z0-9._+-]/-/g')"
if [[ -z "$build_version" ]]; then
  printf '%s\n' 'build version is empty' >&2
  exit 1
fi

npm ci --include=dev --prefix "$repo_root/web"
npm run typecheck --prefix "$repo_root/web"
npm test --prefix "$repo_root/web"
npm run build --prefix "$repo_root/web"
go test ./...

if [[ -z "$repo_root" || "$repo_root" == "/" ]]; then
  printf '%s\n' 'invalid repository root' >&2
  exit 1
fi
rm -rf -- "$repo_root/dist"
mkdir -p -- "$repo_root/dist"
go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub-server" ./cmd/server
go build -trimpath -ldflags "-X confighub.local/internal/buildinfo.Version=$build_version" -o "$repo_root/dist/confighub" ./cmd/cli
