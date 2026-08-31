#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"
source "$repo_root/scripts/package-release.sh"

test_temp_root="$(mktemp -d)"
trap 'rm -rf -- "$test_temp_root"' EXIT

assert_eq "1.2.3" "$(release_version v1.2.3)"
assert_eq "config-hub-server_1.2.3_linux_arm64" \
  "$(release_archive_base server v1.2.3 linux arm64)"
assert_eq "config-hub-cli_1.2.3_linux_amd64.tar.gz" \
  "$(release_archive_name cli v1.2.3 linux amd64)"
assert_eq "config-hub-cli_1.2.3_darwin_arm64.tar.gz" \
  "$(release_archive_name cli v1.2.3 darwin arm64)"
assert_eq "config-hub-cli_1.2.3_windows_amd64.zip" \
  "$(release_archive_name cli v1.2.3 windows amd64)"
assert_eq "confighub" "$(release_binary_name cli darwin)"
assert_eq "confighub.exe" "$(release_binary_name cli windows)"

validate_release_tag v0.0.0
validate_release_tag v12.345.6789
assert_fails validate_release_tag v1.2
assert_fails validate_release_tag 1.2.3
assert_fails validate_release_tag v1.2.3-rc.1
assert_fails validate_release_tag v01.2.3+build
assert_fails validate_release_target server darwin arm64
assert_fails validate_release_target server windows amd64
assert_fails validate_release_target cli darwin amd64
assert_fails validate_release_target cli windows arm64
assert_fails validate_release_target cli linux riscv64
assert_fails release_archive_name web v1.2.3 linux amd64

zip_base="config-hub-cli_1.2.3_windows_amd64"
zip_package_root="$test_temp_root/packages"
zip_output="$test_temp_root/$zip_base.zip"
mkdir -p -- "$zip_package_root/$zip_base"
printf '%s\n' 'fixture executable' >"$zip_package_root/$zip_base/confighub.exe"
create_zip_archive "$zip_package_root" "$zip_base" "$zip_output" 946684800

assert_eq "$(printf '%s\n%s' "$zip_base/" "$zip_base/confighub.exe")" \
  "$(unzip -Z1 "$zip_output")"
zip_details="$(unzip -Z -v "$zip_output")"
assert_contains "$zip_details" 'There is no zipfile comment.'
assert_eq "2" "$(printf '%s\n' "$zip_details" | grep -c '^Central directory entry #')"
assert_eq "2" "$(printf '%s\n' "$zip_details" | grep -c '^  There is no file comment\.$')"

if command -v systemd-analyze >/dev/null 2>&1; then
  verify_dir="$test_temp_root/systemd"
  mkdir -p -- "$verify_dir"
  sed 's#/usr/local/bin/confighub-server#/bin/true#' \
    "$repo_root/deploy/systemd/confighub.service" >"$verify_dir/confighub.service"
  chmod 0644 -- "$verify_dir/confighub.service"
  systemd-analyze verify "$verify_dir/confighub.service"
fi

printf '%s\n' 'package release tests passed'
