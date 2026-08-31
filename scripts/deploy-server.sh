#!/usr/bin/env bash
set -euo pipefail

umask 077

github_repository='art-shier/config-hub'
github_web_root="https://github.com/$github_repository"
release_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
deployment_root=''
requested_version=''
public_url=''
admin_username='admin'
admin_password_file=''
show_help=0
server_temp_root=''
server_stage_path=''
downloaded_server_root=''

die() {
  printf 'deploy-server: %s\n' "$1" >&2
  return 1
}

usage() {
  printf '%s\n' 'Usage: deploy-server.sh [--version vMAJOR.MINOR.PATCH] [--public-url HTTPS_ORIGIN]'
  printf '%s\n' '                        [--admin-username USERNAME] [--admin-password-file FILE]'
}

target_path() {
  local path="$1"
  [[ "$path" == /* ]] || die 'managed path must be absolute' || return
  printf '%s%s\n' "$deployment_root" "$path"
}

validate_release_tag() {
  local tag="${1:-}"
  [[ "$tag" =~ $release_tag_pattern ]] || die 'version must match vMAJOR.MINOR.PATCH'
}

detect_linux_arch() {
  local operating_system="${1:-}"
  local machine="${2:-}"
  [[ "$operating_system" == 'Linux' ]] || die 'only Linux is supported' || return
  case "$machine" in
  x86_64 | amd64) printf '%s\n' 'amd64' ;;
  aarch64 | arm64) printf '%s\n' 'arm64' ;;
  *) die "unsupported Linux architecture: $machine" ;;
  esac
}

validate_public_origin() {
  local value="${1:-}"
  local authority
  local port=''

  [[ "$value" == https://* ]] || die 'public URL must be an absolute HTTPS origin' || return
  authority="${value#https://}"
  if [[ -z "$authority" || "$authority" =~ [/?#@[:space:]] ]]; then
    die 'public URL must not contain credentials, path, query, fragment, or whitespace' || return
  fi

  if [[ "$authority" == \[* ]]; then
    [[ "$authority" =~ ^\[[0-9A-Fa-f:.%]+\](:([0-9]+))?$ ]] ||
      die 'public URL contains an invalid bracketed host' || return
    port="${BASH_REMATCH[2]:-}"
  else
    [[ "$authority" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:([0-9]+))?$ ]] ||
      die 'public URL contains an invalid host' || return
    port="${BASH_REMATCH[3]:-}"
  fi
  if [[ -n "$port" ]]; then
    ((10#$port >= 1 && 10#$port <= 65535)) || die 'public URL port must be from 1 through 65535'
  fi
}

validate_admin_username() {
  local username="${1:-}"
  [[ "$username" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die 'administrator username is invalid'
}

read_password_file() {
  local path="${1:-}"
  local mode
  local size
  local value
  local value_bytes
  local -a lines=()
  local LC_ALL=C

  [[ -n "$path" && -f "$path" && ! -L "$path" ]] ||
    die 'administrator password file must be a regular non-symlink file' || return
  mode="$(stat -c '%a' -- "$path")" || die 'could not read administrator password file mode' || return
  (((8#$mode & 8#077) == 0)) || die 'administrator password file must not grant group or other access' || return
  size="$(stat -c '%s' -- "$path")" || die 'could not read administrator password file size' || return
  ((size > 0 && size <= 4096)) || die 'administrator password file must contain 1 through 4096 bytes' || return
  mapfile -t lines <"$path"
  [[ "${#lines[@]}" -eq 1 ]] || die 'administrator password file must contain exactly one line' || return
  value="${lines[0]}"
  value="${value%$'\r'}"
  [[ -n "$value" && "$value" != *[[:cntrl:]]* ]] ||
    die 'administrator password must be non-empty and contain no control characters' || return
  value_bytes="${#value}"
  ((size == value_bytes || size == value_bytes + 1 || size == value_bytes + 2)) ||
    die 'administrator password file contains unsupported bytes' || return
  printf '%s\n' "$value"
}

read_interactive_password() {
  local first
  local second
  [[ -r /dev/tty && -w /dev/tty ]] || die 'noninteractive deployment requires --admin-password-file' || return
  IFS= read -r -s -p 'Initial administrator password: ' first </dev/tty
  printf '\n' >/dev/tty
  IFS= read -r -s -p 'Confirm administrator password: ' second </dev/tty
  printf '\n' >/dev/tty
  [[ -n "$first" && "$first" == "$second" && "$first" != *[[:cntrl:]]* ]] ||
    die 'administrator passwords are empty, invalid, or do not match' || return
  printf '%s\n' "$first"
}

yaml_double_quote() {
  local value="$1"
  [[ -n "$value" && "$value" != *[[:cntrl:]]* ]] || die 'value cannot be represented safely in YAML' || return
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"\n' "$value"
}

release_tag_from_effective_url() {
  local effective_url="${1:-}"
  local prefix="$github_web_root/releases/tag/"
  local tag
  [[ "$effective_url" == "$prefix"* ]] || die 'latest release redirected outside the ConfigHub repository' || return
  tag="${effective_url#"$prefix"}"
  [[ "$tag" != */* && "$tag" != *\?* && "$tag" != *\#* ]] || die 'latest release redirect is malformed' || return
  validate_release_tag "$tag" || return
  printf '%s\n' "$tag"
}

resolve_latest_tag() {
  local effective_url
  effective_url="$(curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
    --output /dev/null --write-out '%{url_effective}' "$github_web_root/releases/latest")" ||
    die 'could not resolve the latest GitHub Release' || return
  release_tag_from_effective_url "$effective_url"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

download_file() {
  local url="$1"
  local output="$2"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
    --output "$output" "$url"
}

verify_download_checksum() {
  local manifest="$1"
  local archive="$2"
  local archive_name="$3"
  local expected
  local actual
  local -a matches=()

  mapfile -t matches < <(awk -v filename="$archive_name" \
    'NF == 2 && $2 == filename { print $1 }' "$manifest")
  [[ "${#matches[@]}" -eq 1 ]] || die 'checksum manifest must contain one exact archive entry' || return
  expected="${matches[0]}"
  [[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || die 'checksum manifest contains an invalid digest' || return
  actual="$(sha256sum -- "$archive")"
  actual="${actual%% *}"
  [[ "${actual,,}" == "${expected,,}" ]] || die 'archive checksum verification failed'
}

validate_server_archive() {
  local archive="$1"
  local archive_base="$2"
  local extract_root="$3"
  local member
  local listing_line
  local -a members=()
  local -A expected=(
    ["$archive_base/"]=d
    ["$archive_base/confighub-server"]=-
    ["$archive_base/config/"]=d
    ["$archive_base/config/config.example.yaml"]=-
    ["$archive_base/config/users.example.yaml"]=-
    ["$archive_base/deploy/"]=d
    ["$archive_base/deploy/confighub.service"]=-
  )
  local -A seen=()

  mapfile -t members < <(LC_ALL=C tar -tzf "$archive")
  [[ "${#members[@]}" -eq "${#expected[@]}" ]] ||
    die 'Server archive contains an unexpected number of entries' || return
  for member in "${members[@]}"; do
    [[ -n "${expected[$member]+present}" && -z "${seen[$member]+present}" ]] ||
      die "Server archive contains an unsafe or duplicate entry: $member" || return
    seen["$member"]=1
  done

  while IFS= read -r listing_line; do
    case "${listing_line:0:1}" in
    d | -) ;;
    *) die 'Server archive contains a link or special file' || return ;;
    esac
  done < <(LC_ALL=C tar -tvzf "$archive")

  mkdir -p -- "$extract_root"
  tar --no-same-owner --no-same-permissions -xzf "$archive" -C "$extract_root"
  [[ -f "$extract_root/$archive_base/confighub-server" && ! -L "$extract_root/$archive_base/confighub-server" ]] ||
    die 'extracted Server is not a regular file'
}

verify_server_version() {
  local binary="$1"
  local expected="$2"
  local actual
  if ! actual="$("$binary" version 2>/dev/null)"; then
    die 'downloaded Server could not report its version' || return
  fi
  [[ "$actual" == "$expected" ]] || die "Server reported unexpected version: $actual"
}

cleanup_server_download() {
  if [[ -n "${server_stage_path:-}" && -e "$server_stage_path" ]]; then
    unlink -- "$server_stage_path"
  fi
  if [[ -n "${server_temp_root:-}" && -d "$server_temp_root" ]]; then
    rm -rf -- "$server_temp_root"
  fi
}

prepare_server_release() {
  local tag="$1"
  local arch
  local version
  local archive_base
  local archive_name
  local release_url
  local archive_path
  local manifest_path

  validate_release_tag "$tag" || return
  arch="$(detect_linux_arch "$(uname -s)" "$(uname -m)")" || return
  version="${tag#v}"
  archive_base="config-hub-server_${version}_linux_${arch}"
  archive_name="$archive_base.tar.gz"
  release_url="$github_web_root/releases/download/$tag"
  server_temp_root="$(mktemp -d "${TMPDIR:-/tmp}/confighub-server-deploy.XXXXXX")"
  trap cleanup_server_download EXIT INT TERM
  archive_path="$server_temp_root/$archive_name"
  manifest_path="$server_temp_root/checksums.txt"
  download_file "$release_url/$archive_name" "$archive_path"
  download_file "$release_url/checksums.txt" "$manifest_path"
  verify_download_checksum "$manifest_path" "$archive_path" "$archive_name" || return
  validate_server_archive "$archive_path" "$archive_base" "$server_temp_root/extracted" || return
  downloaded_server_root="$server_temp_root/extracted/$archive_base"
  chmod 0755 -- "$downloaded_server_root/confighub-server"
  verify_server_version "$downloaded_server_root/confighub-server" "$tag"
}

ensure_service_account() {
  if getent passwd confighub >/dev/null; then
    getent group confighub >/dev/null || die 'confighub user exists without the confighub group' || return
    [[ "$(id -gn confighub)" == 'confighub' ]] || die 'confighub user has an unexpected primary group' || return
    return 0
  fi
  getent group confighub >/dev/null && die 'confighub group exists without the confighub user' && return 1
  useradd --system --home-dir /var/lib/confighub --shell /usr/sbin/nologin --user-group confighub
}

set_confighub_owner() {
  chown confighub:confighub -- "$@"
}

systemctl_run() {
  systemctl "$@"
}

run_online_backup() {
  local output="$1"
  runuser -u confighub -- "$(target_path /usr/local/bin/confighub-server)" backup \
    --config "$(target_path /etc/confighub/config.yaml)" --output "$output"
}

wait_for_readiness() {
  local attempt
  for attempt in {1..30}; do
    if curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/health/ready >/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

atomic_install() {
  local source="$1"
  local destination="$2"
  local mode="$3"
  local parent
  local stage
  parent="$(dirname -- "$destination")"
  mkdir -p -- "$parent"
  stage="$destination.install.$$"
  [[ ! -e "$stage" ]] || die "temporary install path already exists: $stage" || return
  server_stage_path="$stage"
  install -m "$mode" -- "$source" "$stage"
  mv -f -- "$stage" "$destination"
  server_stage_path=''
}

write_initial_runtime_files() {
  local origin="$1"
  local username="$2"
  local password="$3"
  local config_dir
  local config_path
  local users_path
  local key_path
  local staging
  local quoted_origin
  local quoted_username
  local quoted_password

  config_dir="$(target_path /etc/confighub)"
  config_path="$config_dir/config.yaml"
  users_path="$config_dir/users.yaml"
  key_path="$config_dir/session.key"
  [[ ! -e "$config_path" && ! -e "$users_path" && ! -e "$key_path" ]] ||
    die 'existing Server configuration will not be overwritten' || return
  quoted_origin="$(yaml_double_quote "$origin")" || return
  quoted_username="$(yaml_double_quote "$username")" || return
  quoted_password="$(yaml_double_quote "$password")" || return
  staging="$(mktemp -d "$config_dir/.bootstrap.XXXXXX")"

  {
    printf '%s\n' 'server:'
    printf '%s\n' '  listen: 127.0.0.1:8080'
    printf '  public_url: %s\n' "$quoted_origin"
    printf '%s\n' '  trusted_proxy_cidrs:'
    printf '%s\n' '    - 127.0.0.1/32'
    printf '%s\n' '    - ::1/128'
    printf '%s\n' 'database:'
    printf '%s\n' '  path: /var/lib/confighub/confighub.db'
    printf '%s\n' 'auth:'
    printf '%s\n' '  users_file: /etc/confighub/users.yaml'
    printf '%s\n' '  session_key_file: /etc/confighub/session.key'
    printf '%s\n' '  session_ttl: 24h'
    printf '%s\n' 'backup:'
    printf '%s\n' '  directory: /var/backups/confighub'
  } >"$staging/config.yaml"
  {
    printf '%s\n' 'users:'
    printf '  - username: %s\n' "$quoted_username"
    printf '%s\n' '    display_name: "Administrator"'
    printf '    password: %s\n' "$quoted_password"
    printf '%s\n' '    role: admin'
    printf '%s\n' '    enabled: true'
  } >"$staging/users.yaml"
  openssl rand -base64 48 >"$staging/session.key"
  chmod 0600 -- "$staging/config.yaml" "$staging/users.yaml" "$staging/session.key"
  mv -- "$staging/config.yaml" "$config_path"
  mv -- "$staging/users.yaml" "$users_path"
  mv -- "$staging/session.key" "$key_path"
  rmdir -- "$staging"
  set_confighub_owner "$config_path" "$users_path" "$key_path"
}

install_first_server() {
  local extracted_root="$1"
  local tag="$2"
  local origin="$3"
  local username="$4"
  local password="$5"
  local config_dir
  local data_dir
  local backup_dir
  local binary_path
  local unit_path

  validate_release_tag "$tag" || return
  validate_public_origin "$origin" || return
  validate_admin_username "$username" || return
  [[ -n "$password" && "$password" != *[[:cntrl:]]* ]] || die 'administrator password is invalid' || return
  config_dir="$(target_path /etc/confighub)"
  data_dir="$(target_path /var/lib/confighub)"
  backup_dir="$(target_path /var/backups/confighub)"
  binary_path="$(target_path /usr/local/bin/confighub-server)"
  unit_path="$(target_path /etc/systemd/system/confighub.service)"
  [[ ! -e "$binary_path" && ! -e "$unit_path" && ! -e "$config_dir/config.yaml" &&
    ! -e "$config_dir/users.yaml" && ! -e "$config_dir/session.key" ]] ||
    die 'existing managed Server files prevent a first installation' || return
  verify_server_version "$extracted_root/confighub-server" "$tag" || return

  ensure_service_account || return
  install -d -m 0700 -- "$config_dir" "$data_dir" "$backup_dir"
  set_confighub_owner "$config_dir" "$data_dir" "$backup_dir"
  write_initial_runtime_files "$origin" "$username" "$password" || return
  atomic_install "$extracted_root/confighub-server" "$binary_path" 0755 || return
  atomic_install "$extracted_root/deploy/confighub.service" "$unit_path" 0644 || return
  systemctl_run daemon-reload
  systemctl_run enable --now confighub.service
  if ! wait_for_readiness; then
    printf '%s\n' 'deploy-server: Server did not become ready; inspect: journalctl -u confighub.service' >&2
    return 1
  fi
  printf 'Deployed ConfigHub Server %s. Configure the external HTTPS reverse proxy for %s.\n' "$tag" "$origin"
}

managed_install_state() {
  local -a paths=(
    "$(target_path /usr/local/bin/confighub-server)"
    "$(target_path /etc/confighub/config.yaml)"
    "$(target_path /etc/confighub/users.yaml)"
    "$(target_path /etc/confighub/session.key)"
    "$(target_path /etc/systemd/system/confighub.service)"
  )
  local path
  local count=0
  for path in "${paths[@]}"; do
    [[ ! -e "$path" ]] || count=$((count + 1))
  done
  case "$count" in
  0) printf '%s\n' initial ;;
  5) printf '%s\n' managed ;;
  *) die 'partial managed installation detected; repair it before deploying' ;;
  esac
}

upgrade_server() {
  local extracted_root="$1"
  local tag="$2"
  local binary_path
  local config_path
  local unit_path
  local previous_path
  local backup_dir
  local backup_path
  local current_version
  local mode

  validate_release_tag "$tag" || return
  [[ "$(managed_install_state)" == managed ]] || die 'upgrade requires a complete managed installation' || return
  binary_path="$(target_path /usr/local/bin/confighub-server)"
  config_path="$(target_path /etc/confighub/config.yaml)"
  unit_path="$(target_path /etc/systemd/system/confighub.service)"
  previous_path="$(target_path /usr/local/lib/confighub/confighub-server.previous)"
  backup_dir="$(target_path /var/backups/confighub)"
  verify_server_version "$extracted_root/confighub-server" "$tag" || return
  current_version="$("$binary_path" version)" || die 'installed Server could not report its version' || return

  if [[ "$current_version" == "$tag" ]]; then
    if ! wait_for_readiness; then
      die 'installed Server version matches but readiness is failing' || return
    fi
    printf 'ConfigHub Server %s is already installed and ready.\n' "$tag"
    return 0
  fi

  install -d -m 0700 -- "$backup_dir"
  set_confighub_owner "$backup_dir"
  backup_path="$backup_dir/confighub-pre-upgrade-$(date -u '+%Y%m%d-%H%M%S').db"
  run_online_backup "$backup_path" || die 'online backup failed; Server was not changed' || return
  [[ -f "$backup_path" && ! -L "$backup_path" ]] || die 'online backup did not create a regular file' || return
  mode="$(stat -c '%a' -- "$backup_path")"
  (((8#$mode & 8#077) == 0)) || die 'online backup permissions are unsafe' || return
  set_confighub_owner "$backup_path"

  install -d -m 0755 -- "$(dirname -- "$previous_path")"
  atomic_install "$binary_path" "$previous_path" 0755 || return
  atomic_install "$extracted_root/deploy/confighub.service" "$unit_path" 0644 || return
  atomic_install "$extracted_root/confighub-server" "$binary_path" 0755 || return
  systemctl_run daemon-reload
  systemctl_run restart confighub.service
  if ! wait_for_readiness; then
    printf '%s\n' 'deploy-server: upgraded Server did not become ready.' >&2
    printf 'deploy-server: inspect: journalctl -u confighub.service\n' >&2
    printf 'deploy-server: previous binary: /usr/local/lib/confighub/confighub-server.previous\n' >&2
    printf 'deploy-server: pre-upgrade backup: /var/backups/confighub/%s\n' "${backup_path##*/}" >&2
    printf '%s\n' 'deploy-server: SQLite was not rolled back automatically.' >&2
    return 1
  fi
  printf 'Upgraded ConfigHub Server from %s to %s; backup: /var/backups/confighub/%s\n' \
    "$current_version" "$tag" "${backup_path##*/}"
}

parse_server_args() {
  requested_version=''
  public_url=''
  admin_username='admin'
  admin_password_file=''
  show_help=0
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
    --version | --public-url | --admin-username | --admin-password-file)
      [[ "$#" -ge 2 ]] || die "$1 requires a value" || return
      case "$1" in
      --version) requested_version="$2" ;;
      --public-url) public_url="$2" ;;
      --admin-username) admin_username="$2" ;;
      --admin-password-file) admin_password_file="$2" ;;
      esac
      shift 2
      ;;
    --help | -h)
      show_help=1
      shift
      ;;
    *) die "unknown argument: $1" || return ;;
    esac
  done
  [[ -z "$requested_version" ]] || validate_release_tag "$requested_version" || return
  [[ -z "$public_url" ]] || validate_public_origin "$public_url" || return
  validate_admin_username "$admin_username"
}

require_production_host() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || die 'Server deployment must run as root' || return
  [[ -d /run/systemd/system ]] || die 'Server deployment requires systemd as PID 1' || return
}

main() {
  local command_name
  local state
  local tag
  local password

  parse_server_args "$@" || return 2
  if [[ "$show_help" -eq 1 ]]; then
    usage
    return 0
  fi
  require_production_host || return
  for command_name in curl sha256sum tar awk mktemp install openssl stat systemctl useradd getent id runuser; do
    require_command "$command_name" || return
  done
  state="$(managed_install_state)" || return
  if [[ "$state" == initial && -z "$public_url" ]]; then
    die 'first deployment requires --public-url' || return 2
  fi
  if [[ -n "$requested_version" ]]; then
    tag="$requested_version"
  else
    tag="$(resolve_latest_tag)" || return
  fi
  prepare_server_release "$tag" || return

  if [[ "$state" == managed ]]; then
    upgrade_server "$downloaded_server_root" "$tag"
  else
    if [[ -n "$admin_password_file" ]]; then
      password="$(read_password_file "$admin_password_file")" || return
    else
      password="$(read_interactive_password)" || return
    fi
    install_first_server "$downloaded_server_root" "$tag" "$public_url" "$admin_username" "$password"
  fi
}

if [[ -z "${BASH_SOURCE[0]:-}" || "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
