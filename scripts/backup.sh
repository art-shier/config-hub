#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
config_path="${CONFIGHUB_CONFIG:-$repo_root/config/config.yaml}"
backup_dir="$repo_root/backups"
timestamp="$(date -u '+%Y%m%d-%H%M%S')"
output_path="$backup_dir/confighub-$timestamp.db"

exec "$repo_root/dist/confighub-server" backup --config "$config_path" --output "$output_path"
