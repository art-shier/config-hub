#!/usr/bin/env bash
set -euo pipefail

test_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$test_dir/../.." && pwd)"

# shellcheck source=scripts/tests/testlib.sh
source "$test_dir/testlib.sh"
source "$repo_root/scripts/deploy-server.sh"

validate_public_origin https://config.example.com
validate_public_origin https://config.example.com:8443
validate_public_origin 'https://[2001:db8::1]:8443'
assert_fails validate_public_origin http://config.example.com
assert_fails validate_public_origin https://user@config.example.com
assert_fails validate_public_origin https://config.example.com/path
assert_fails validate_public_origin 'https://config.example.com?query=value'
assert_fails validate_public_origin https://config.example.com:0
assert_fails validate_public_origin https://config.example.com:65536

validate_admin_username admin
validate_admin_username 'release-admin_1'
assert_fails validate_admin_username '-admin'
assert_fails validate_admin_username "$(printf 'a%.0s' {1..65})"

fixture_root="$(mktemp -d)"
trap 'rm -rf -- "$fixture_root"' EXIT
password_file="$fixture_root/password"
printf '%s\n' 'safe password: # "quote" \ slash' >"$password_file"
chmod 0600 -- "$password_file"
password="$(read_password_file "$password_file")"
assert_eq 'safe password: # "quote" \ slash' "$password"
assert_eq '"safe password: # \"quote\" \\ slash"' "$(yaml_double_quote "$password")"
chmod 0640 -- "$password_file"
assert_fails read_password_file "$password_file"
chmod 0600 -- "$password_file"
ln -s -- "$password_file" "$fixture_root/password-link"
assert_fails read_password_file "$fixture_root/password-link"
printf '\n' >"$fixture_root/empty-password"
chmod 0600 -- "$fixture_root/empty-password"
assert_fails read_password_file "$fixture_root/empty-password"
printf 'first\nsecond\n' >"$fixture_root/multiline-password"
chmod 0600 -- "$fixture_root/multiline-password"
assert_fails read_password_file "$fixture_root/multiline-password"

make_server_fixture() {
  local version="$1"
  local output="$2"
  mkdir -p -- "$output/config" "$output/deploy"
  cat >"$output/confighub-server" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == version && "\$#" -eq 1 ]]; then
  printf '%s\\n' '$version'
  exit 0
fi
exit 2
EOF
  chmod 0755 -- "$output/confighub-server"
  cp -- "$repo_root/config/config.example.yaml" "$output/config/config.example.yaml"
  cp -- "$repo_root/config/users.example.yaml" "$output/config/users.example.yaml"
  cp -- "$repo_root/deploy/systemd/confighub.service" "$output/deploy/confighub.service"
}

v1_fixture="$fixture_root/v1"
v2_fixture="$fixture_root/v2"
v3_fixture="$fixture_root/v3"
make_server_fixture v1.2.3 "$v1_fixture"
make_server_fixture v1.2.4 "$v2_fixture"
make_server_fixture v1.2.5 "$v3_fixture"

archive_base=config-hub-server_1.2.3_linux_amd64
archive_root="$fixture_root/archive"
mkdir -p -- "$archive_root/$archive_base"
cp -R -- "$v1_fixture/." "$archive_root/$archive_base/"
tar -czf "$fixture_root/server.tar.gz" -C "$archive_root" "$archive_base"
validate_server_archive "$fixture_root/server.tar.gz" "$archive_base" "$fixture_root/server-extracted"
assert_file "$fixture_root/server-extracted/$archive_base/confighub-server"
printf '%s\n' unexpected >"$archive_root/$archive_base/extra"
tar -czf "$fixture_root/server-extra.tar.gz" -C "$archive_root" "$archive_base"
assert_fails validate_server_archive "$fixture_root/server-extra.tar.gz" "$archive_base" \
  "$fixture_root/server-extra-extracted"

deployment_root="$fixture_root/root"
systemctl_log="$fixture_root/systemctl.log"
ensure_service_account() { :; }
set_confighub_owner() { :; }
systemctl_run() { printf '%s\n' "$*" >>"$systemctl_log"; }
wait_for_readiness() { return 0; }

install_first_server "$v1_fixture" v1.2.3 https://config.example.com admin "$password"
assert_eq managed "$(managed_install_state)"

assert_file "$deployment_root/usr/local/bin/confighub-server"
assert_file "$deployment_root/etc/confighub/config.yaml"
assert_file "$deployment_root/etc/confighub/users.yaml"
assert_file "$deployment_root/etc/confighub/session.key"
assert_file "$deployment_root/etc/systemd/system/confighub.service"
assert_contains "$(cat "$deployment_root/etc/confighub/config.yaml")" 'listen: 127.0.0.1:8080'
assert_contains "$(cat "$deployment_root/etc/confighub/config.yaml")" 'public_url: "https://config.example.com"'
assert_contains "$(cat "$deployment_root/etc/confighub/config.yaml")" 'path: /var/lib/confighub/confighub.db'
assert_contains "$(cat "$deployment_root/etc/confighub/users.yaml")" 'username: "admin"'
assert_contains "$(cat "$deployment_root/etc/confighub/users.yaml")" \
  'password: "safe password: # \"quote\" \\ slash"'
assert_eq 700 "$(stat -c '%a' "$deployment_root/etc/confighub")"
assert_eq 700 "$(stat -c '%a' "$deployment_root/var/lib/confighub")"
assert_eq 700 "$(stat -c '%a' "$deployment_root/var/backups/confighub")"
assert_eq 600 "$(stat -c '%a' "$deployment_root/etc/confighub/config.yaml")"
assert_eq 600 "$(stat -c '%a' "$deployment_root/etc/confighub/users.yaml")"
assert_eq 600 "$(stat -c '%a' "$deployment_root/etc/confighub/session.key")"
assert_eq 755 "$(stat -c '%a' "$deployment_root/usr/local/bin/confighub-server")"
[[ "$(tr -d '\r\n' <"$deployment_root/etc/confighub/session.key" | wc -c)" -ge 32 ]] ||
  fail 'session key is shorter than 32 bytes'
assert_contains "$(cat "$systemctl_log")" 'enable --now confighub.service'

config_before="$(sha256sum "$deployment_root/etc/confighub/config.yaml")"
users_before="$(sha256sum "$deployment_root/etc/confighub/users.yaml")"
key_before="$(sha256sum "$deployment_root/etc/confighub/session.key")"
assert_fails install_first_server "$v2_fixture" v1.2.4 https://other.example.com other changed
assert_eq "$config_before" "$(sha256sum "$deployment_root/etc/confighub/config.yaml")"

# shellcheck disable=SC2317
run_online_backup() { return 1; }
assert_fails upgrade_server "$v2_fixture" v1.2.4
assert_eq v1.2.3 "$("$deployment_root/usr/local/bin/confighub-server" version)"
assert_not_file "$deployment_root/usr/local/lib/confighub/confighub-server.previous"

run_online_backup() {
  local output="$1"
  printf '%s\n' backup >"$output"
  chmod 0600 -- "$output"
}
upgrade_server "$v2_fixture" v1.2.4
assert_eq v1.2.4 "$("$deployment_root/usr/local/bin/confighub-server" version)"
assert_eq v1.2.3 "$("$deployment_root/usr/local/lib/confighub/confighub-server.previous" version)"
assert_eq "$config_before" "$(sha256sum "$deployment_root/etc/confighub/config.yaml")"
assert_eq "$users_before" "$(sha256sum "$deployment_root/etc/confighub/users.yaml")"
assert_eq "$key_before" "$(sha256sum "$deployment_root/etc/confighub/session.key")"

wait_for_readiness() { return 1; }
if failure_output="$(upgrade_server "$v3_fixture" v1.2.5 2>&1)"; then
  fail 'upgrade unexpectedly passed failed readiness'
fi
assert_contains "$failure_output" 'journalctl -u confighub.service'
assert_contains "$failure_output" '/usr/local/lib/confighub/confighub-server.previous'
assert_contains "$failure_output" '/var/backups/confighub/'
[[ "$failure_output" != *"$password"* ]] || fail 'upgrade failure leaked the administrator password'
assert_eq v1.2.5 "$("$deployment_root/usr/local/bin/confighub-server" version)"
assert_eq v1.2.4 "$("$deployment_root/usr/local/lib/confighub/confighub-server.previous" version)"

backup_calls=0
run_online_backup() { backup_calls=$((backup_calls + 1)); }
wait_for_readiness() { return 0; }
upgrade_server "$v3_fixture" v1.2.5
assert_eq 0 "$backup_calls"

printf '%s\n' 'Server deployment tests passed'
