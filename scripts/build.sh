#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"

cd "$repo_root"
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
