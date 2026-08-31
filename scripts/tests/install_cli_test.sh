#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"
source "$repo_root/scripts/install-cli.sh"

assert_eq "linux amd64" "$(detect_cli_target Linux x86_64)"
assert_eq "linux amd64" "$(detect_cli_target Linux amd64)"
assert_eq "linux arm64" "$(detect_cli_target Linux aarch64)"
assert_eq "linux arm64" "$(detect_cli_target Linux arm64)"
assert_eq "darwin arm64" "$(detect_cli_target Darwin arm64)"
assert_eq "darwin arm64" "$(detect_cli_target Darwin aarch64)"
assert_fails detect_cli_target Darwin x86_64
assert_fails detect_cli_target Windows_NT AMD64
assert_fails detect_cli_target Linux riscv64
assert_fails validate_release_tag v1.2.3-rc1
assert_eq "v1.2.3" "$(release_tag_from_effective_url \
  https://github.com/art-shier/config-hub/releases/tag/v1.2.3)"
assert_fails release_tag_from_effective_url \
  https://github.com/another/config-hub/releases/tag/v1.2.3
assert_fails parse_cli_args --install-dir relative/path
assert_fails parse_cli_args --unknown

fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT

checksum_fixture="$fixture_root/checksum-fixture.txt"
checksum_expected='1805cc9ea64598bb6c71c9a14232b932d9369da74d2e3943342cf2d2a05fc609'
printf '%s\n' 'ConfigHub checksum fixture' >"$checksum_fixture"
assert_eq "$checksum_expected" "$(sha256_file "$checksum_fixture")"

fallback_bin="$fixture_root/fallback-bin"
mkdir -p -- "$fallback_bin"
cp -- "$(command -v awk)" "$fallback_bin/awk"
cat >"$fallback_bin/shasum" <<EOF
#!/bin/bash
printf '%s  %s\n' '1805CC9EA64598BB6C71C9A14232B932D9369DA74D2E3943342CF2D2A05FC609' "\${4:-}"
EOF
chmod 0755 -- "$fallback_bin/awk" "$fallback_bin/shasum"
assert_eq "$checksum_expected" "$(PATH="$fallback_bin" sha256_file "$checksum_fixture")"

fixture_target="$(detect_cli_target "$(uname -s)" "$(uname -m)")"
read -r fixture_operating_system fixture_architecture <<<"$fixture_target"
# The production function is sourced above; the test override is installed later.
# shellcheck disable=SC2218
remove_download_origin_mark "$fixture_operating_system" "$checksum_fixture"

fixture_base="config-hub-cli_1.2.3_${fixture_operating_system}_${fixture_architecture}"
mkdir -p -- "$fixture_root/package/$fixture_base" "$fixture_root/release"
cat >"$fixture_root/package/$fixture_base/confighub" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == version && "$#" -eq 1 ]]; then
  if [[ -n "${CLI_INSTALL_TEST_EVENT_LOG:-}" ]]; then
    printf '%s\n' 'version' >>"$CLI_INSTALL_TEST_EVENT_LOG"
  fi
  printf '%s\n' 'v1.2.3'
  exit 0
fi
exit 2
EOF
chmod 0755 -- "$fixture_root/package/$fixture_base/confighub"
tar -czf "$fixture_root/release/$fixture_base.tar.gz" -C "$fixture_root/package" "$fixture_base"
(
  cd "$fixture_root/release"
  printf '%s  %s\n' "$(sha256_file "$fixture_base.tar.gz")" "$fixture_base.tar.gz" >checksums.txt
)

download_file() {
  local url="$1"
  local output="$2"
  cp -- "$fixture_root/release/${url##*/}" "$output"
}

event_log="$fixture_root/install-events"
export CLI_INSTALL_TEST_EVENT_LOG="$event_log"
remove_download_origin_mark() {
  local operating_system="$1"
  local file="$2"
  [[ "$operating_system" == "$fixture_operating_system" ]] ||
    fail "unexpected origin-mark platform: $operating_system"
  [[ -f "$file" && ! -L "$file" ]] || fail "origin-mark target is not a regular file: $file"
  printf '%s\n' 'origin-mark' >>"$event_log"
}

install_dir="$fixture_root/install"
install_cli_release v1.2.3 "$install_dir"
assert_file "$install_dir/confighub"
assert_eq "v1.2.3" "$("$install_dir/confighub" version)"
assert_eq "$(printf '%s\n%s' 'origin-mark' 'version')" \
  "$(sed -n '1,2p' "$event_log")"

printf '%s\n' 'old-cli' >"$install_dir/confighub"
cp -- "$fixture_root/release/checksums.txt" "$fixture_root/release/checksums.good"
printf '%064d  %s.tar.gz\n' 0 "$fixture_base" >"$fixture_root/release/checksums.txt"
rm -f -- "$event_log"
assert_fails install_cli_release v1.2.3 "$install_dir"
assert_eq "old-cli" "$(cat "$install_dir/confighub")"
assert_not_file "$event_log"
mv -- "$fixture_root/release/checksums.good" "$fixture_root/release/checksums.txt"

malicious_root="$fixture_root/malicious"
mkdir -p -- "$malicious_root/$fixture_base"
cp -- "$fixture_root/package/$fixture_base/confighub" "$malicious_root/$fixture_base/confighub"
printf '%s\n' unexpected >"$malicious_root/$fixture_base/extra"
tar -czf "$fixture_root/extra.tar.gz" -C "$malicious_root" "$fixture_base"
assert_fails validate_cli_archive "$fixture_root/extra.tar.gz" "$fixture_base" "$fixture_root/extract-extra"

link_root="$fixture_root/link"
mkdir -p -- "$link_root/$fixture_base"
ln -s -- /bin/true "$link_root/$fixture_base/confighub"
tar -czf "$fixture_root/link.tar.gz" -C "$link_root" "$fixture_base"
assert_fails validate_cli_archive "$fixture_root/link.tar.gz" "$fixture_base" "$fixture_root/extract-link"

printf '%s\n' 'CLI installer tests passed'
