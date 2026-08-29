#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
config_path="${CONFIGHUB_CONFIG:-$repo_root/config/config.yaml}"

exec "$repo_root/dist/confighub-server" serve --config "$config_path"
