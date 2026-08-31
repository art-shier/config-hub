#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"
source "$repo_root/scripts/package-release.sh"

assert_eq "1.2.3" "$(release_version v1.2.3)"
assert_eq "config-hub-server_1.2.3_linux_arm64" \
  "$(release_archive_base server v1.2.3 arm64)"
assert_eq "config-hub-cli_1.2.3_linux_amd64" \
  "$(release_archive_base cli v1.2.3 amd64)"

validate_release_tag v0.0.0
validate_release_tag v12.345.6789
assert_fails validate_release_tag v1.2
assert_fails validate_release_tag 1.2.3
assert_fails validate_release_tag v1.2.3-rc.1
assert_fails validate_release_tag v01.2.3+build
assert_fails release_archive_base web v1.2.3 amd64
assert_fails release_archive_base cli v1.2.3 riscv64

if command -v systemd-analyze >/dev/null 2>&1; then
  verify_dir="$(mktemp -d)"
  trap 'rm -rf -- "$verify_dir"' EXIT
  sed 's#/usr/local/bin/confighub-server#/bin/true#' \
    "$repo_root/deploy/systemd/confighub.service" >"$verify_dir/confighub.service"
  chmod 0644 -- "$verify_dir/confighub.service"
  systemd-analyze verify "$verify_dir/confighub.service"
fi

printf '%s\n' 'package release tests passed'
