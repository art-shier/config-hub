#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"

fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT
fake_bin="$fixture_root/bin"
mkdir -p -- "$fake_bin"

cat >"$fake_bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

state="${FAKE_GH_STATE_DIR:?}"
mkdir -p -- "$state"
[[ "${1:-}" == release ]] || exit 2
operation="${2:-}"
tag="${3:-}"

case "$operation" in
view)
  [[ -f "$state/release" ]] || exit 1
  if [[ " $* " == *' --json isDraft '* ]]; then
    cat "$state/draft"
  elif [[ " $* " == *' --json assets '* ]]; then
    cat "$state/assets"
  fi
  ;;
create)
  [[ ! -e "$state/release" ]] || exit 1
  : >"$state/release"
  printf '%s\n' true >"$state/draft"
  : >"$state/assets"
  ;;
upload)
  [[ -f "$state/release" ]] || exit 1
  [[ "${FAKE_GH_FAIL_UPLOAD:-0}" != 1 ]] || exit 1
  shift 3
  : >"$state/assets"
  for argument in "$@"; do
    [[ "$argument" != --repo ]] || break
    printf '%s\n' "${argument##*/}" >>"$state/assets"
  done
  LC_ALL=C sort -o "$state/assets" "$state/assets"
  if [[ "${FAKE_GH_REMOTE_MISMATCH:-0}" == 1 ]]; then
    printf '%s\n' unexpected-asset >>"$state/assets"
  fi
  ;;
edit)
  [[ -f "$state/release" ]] || exit 1
  printf '%s\n' false >"$state/draft"
  ;;
delete)
  [[ -f "$state/release" ]] || exit 1
  rm -f -- "$state/release" "$state/draft" "$state/assets"
  : >"$state/deleted"
  ;;
*) exit 2 ;;
esac
EOF
chmod 0755 -- "$fake_bin/gh"

make_release_fixture() {
  local output="$1"
  mkdir -p -- "$output"
  printf '%s\n' server-amd64 >"$output/config-hub-server_1.2.3_linux_amd64.tar.gz"
  printf '%s\n' server-arm64 >"$output/config-hub-server_1.2.3_linux_arm64.tar.gz"
  printf '%s\n' cli-amd64 >"$output/config-hub-cli_1.2.3_linux_amd64.tar.gz"
  printf '%s\n' cli-arm64 >"$output/config-hub-cli_1.2.3_linux_arm64.tar.gz"
  printf '%s\n' cli-darwin-arm64 >"$output/config-hub-cli_1.2.3_darwin_arm64.tar.gz"
  printf '%s\n' cli-windows-amd64 >"$output/config-hub-cli_1.2.3_windows_amd64.zip"
  (
    cd "$output"
    sha256sum config-hub-*.tar.gz config-hub-*.zip | LC_ALL=C sort -k2 >checksums.txt
  )
}

expected_assets=$'checksums.txt\nconfig-hub-cli_1.2.3_darwin_arm64.tar.gz\nconfig-hub-cli_1.2.3_linux_amd64.tar.gz\nconfig-hub-cli_1.2.3_linux_arm64.tar.gz\nconfig-hub-cli_1.2.3_windows_amd64.zip\nconfig-hub-server_1.2.3_linux_amd64.tar.gz\nconfig-hub-server_1.2.3_linux_arm64.tar.gz'

success_release="$fixture_root/success-release"
success_state="$fixture_root/success-state"
make_release_fixture "$success_release"
FAKE_GH_STATE_DIR="$success_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$success_release"
assert_file "$success_state/release"
assert_eq false "$(cat "$success_state/draft")"
assert_eq "$expected_assets" "$(cat "$success_state/assets")"
assert_not_file "$success_state/deleted"

existing_release="$fixture_root/existing-release"
existing_state="$fixture_root/existing-state"
make_release_fixture "$existing_release"
mkdir -p -- "$existing_state"
: >"$existing_state/release"
printf '%s\n' false >"$existing_state/draft"
: >"$existing_state/assets"
assert_fails env FAKE_GH_STATE_DIR="$existing_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$existing_release"
assert_file "$existing_state/release"
assert_not_file "$existing_state/deleted"

missing_darwin_release="$fixture_root/missing-darwin-release"
missing_darwin_state="$fixture_root/missing-darwin-state"
make_release_fixture "$missing_darwin_release"
rm -f -- "$missing_darwin_release/config-hub-cli_1.2.3_darwin_arm64.tar.gz"
assert_fails env FAKE_GH_STATE_DIR="$missing_darwin_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$missing_darwin_release"
assert_not_file "$missing_darwin_state/release"

missing_windows_release="$fixture_root/missing-windows-release"
missing_windows_state="$fixture_root/missing-windows-state"
make_release_fixture "$missing_windows_release"
rm -f -- "$missing_windows_release/config-hub-cli_1.2.3_windows_amd64.zip"
assert_fails env FAKE_GH_STATE_DIR="$missing_windows_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$missing_windows_release"
assert_not_file "$missing_windows_state/release"

extra_release="$fixture_root/extra-release"
extra_state="$fixture_root/extra-state"
make_release_fixture "$extra_release"
printf '%s\n' old-style >"$extra_release/config-hub-cli_1.2.3_windows_amd64.tar.gz"
assert_fails env FAKE_GH_STATE_DIR="$extra_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$extra_release"
assert_not_file "$extra_state/release"

missing_checksum_release="$fixture_root/missing-checksum-release"
missing_checksum_state="$fixture_root/missing-checksum-state"
make_release_fixture "$missing_checksum_release"
awk '$2 != "config-hub-cli_1.2.3_windows_amd64.zip"' \
  "$missing_checksum_release/checksums.txt" >"$missing_checksum_release/checksums.filtered"
mv -- "$missing_checksum_release/checksums.filtered" "$missing_checksum_release/checksums.txt"
assert_fails env FAKE_GH_STATE_DIR="$missing_checksum_state" PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$missing_checksum_release"
assert_not_file "$missing_checksum_state/release"

upload_release="$fixture_root/upload-release"
upload_state="$fixture_root/upload-state"
make_release_fixture "$upload_release"
assert_fails env FAKE_GH_STATE_DIR="$upload_state" FAKE_GH_FAIL_UPLOAD=1 PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$upload_release"
assert_not_file "$upload_state/release"
assert_file "$upload_state/deleted"

mismatch_release="$fixture_root/mismatch-release"
mismatch_state="$fixture_root/mismatch-state"
make_release_fixture "$mismatch_release"
assert_fails env FAKE_GH_STATE_DIR="$mismatch_state" FAKE_GH_REMOTE_MISMATCH=1 PATH="$fake_bin:$PATH" \
  bash "$repo_root/scripts/publish-release.sh" v1.2.3 "$mismatch_release"
assert_not_file "$mismatch_state/release"
assert_file "$mismatch_state/deleted"

printf '%s\n' 'Release publication tests passed'
