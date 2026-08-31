#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"
source "$repo_root/scripts/install-cli.sh"

assert_eq "amd64" "$(detect_linux_arch Linux x86_64)"
assert_eq "amd64" "$(detect_linux_arch Linux amd64)"
assert_eq "arm64" "$(detect_linux_arch Linux aarch64)"
assert_eq "arm64" "$(detect_linux_arch Linux arm64)"
assert_fails detect_linux_arch Darwin arm64
assert_fails detect_linux_arch Linux riscv64
assert_fails validate_release_tag v1.2.3-rc1
assert_eq "v1.2.3" "$(release_tag_from_effective_url \
  https://github.com/art-shier/config-hub/releases/tag/v1.2.3)"
assert_fails release_tag_from_effective_url \
  https://github.com/another/config-hub/releases/tag/v1.2.3
assert_fails parse_cli_args --install-dir relative/path
assert_fails parse_cli_args --unknown

fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT
fixture_base=config-hub-cli_1.2.3_linux_amd64
mkdir -p -- "$fixture_root/package/$fixture_base" "$fixture_root/release"
cat >"$fixture_root/package/$fixture_base/confighub" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == version && "$#" -eq 1 ]]; then
  printf '%s\n' 'v1.2.3'
  exit 0
fi
exit 2
EOF
chmod 0755 -- "$fixture_root/package/$fixture_base/confighub"
tar -czf "$fixture_root/release/$fixture_base.tar.gz" -C "$fixture_root/package" "$fixture_base"
(
  cd "$fixture_root/release"
  sha256sum "$fixture_base.tar.gz" >checksums.txt
)

download_file() {
  local url="$1"
  local output="$2"
  cp -- "$fixture_root/release/${url##*/}" "$output"
}

install_dir="$fixture_root/install"
install_cli_release v1.2.3 "$install_dir"
assert_file "$install_dir/confighub"
assert_eq "v1.2.3" "$("$install_dir/confighub" version)"

printf '%s\n' 'old-cli' >"$install_dir/confighub"
cp -- "$fixture_root/release/checksums.txt" "$fixture_root/release/checksums.good"
printf '%064d  %s.tar.gz\n' 0 "$fixture_base" >"$fixture_root/release/checksums.txt"
assert_fails install_cli_release v1.2.3 "$install_dir"
assert_eq "old-cli" "$(cat "$install_dir/confighub")"
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
