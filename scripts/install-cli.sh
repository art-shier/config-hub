#!/usr/bin/env bash
set -euo pipefail

umask 077

github_repository='art-shier/config-hub'
github_web_root="https://github.com/$github_repository"
release_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
requested_version=''
install_dir='/usr/local/bin'
show_help=0
cli_temp_root=''
cli_stage_path=''

die() {
  printf 'install-cli: %s\n' "$1" >&2
  return 1
}

usage() {
  printf '%s\n' 'Usage: install-cli.sh [--version vMAJOR.MINOR.PATCH] [--install-dir ABSOLUTE_DIRECTORY]'
}

validate_release_tag() {
  local tag="${1:-}"
  [[ "$tag" =~ $release_tag_pattern ]] || die 'version must match vMAJOR.MINOR.PATCH'
}

detect_cli_target() {
  local operating_system="${1:-}"
  local machine="${2:-}"

  case "$operating_system/$machine" in
  Linux/x86_64 | Linux/amd64) printf '%s\n' 'linux amd64' ;;
  Linux/aarch64 | Linux/arm64) printf '%s\n' 'linux arm64' ;;
  Darwin/aarch64 | Darwin/arm64) printf '%s\n' 'darwin arm64' ;;
  *) die "unsupported CLI target: $operating_system/$machine" ;;
  esac
}

release_tag_from_effective_url() {
  local effective_url="${1:-}"
  local prefix="$github_web_root/releases/tag/"
  local tag

  [[ "$effective_url" == "$prefix"* ]] || die 'latest release redirected outside the ConfigHub repository' || return
  tag="${effective_url#"$prefix"}"
  [[ "$tag" != */* && "$tag" != *\?* && "$tag" != *\#* ]] ||
    die 'latest release redirect is malformed' || return
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

sha256_file() {
  local file="$1"

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$file" | awk '{ print tolower($1) }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -- "$file" | awk '{ print tolower($1) }'
  else
    die 'required SHA-256 command not found: sha256sum or shasum'
  fi
}

verify_download_checksum() {
  local manifest="$1"
  local archive="$2"
  local archive_name="$3"
  local expected
  local actual
  local match_count

  match_count="$(awk -v filename="$archive_name" \
    'NF == 2 && $2 == filename { count++ } END { print count + 0 }' "$manifest")"
  [[ "$match_count" -eq 1 ]] || die 'checksum manifest must contain one exact archive entry' || return
  expected="$(awk -v filename="$archive_name" \
    'NF == 2 && $2 == filename { print $1 }' "$manifest")"
  [[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || die 'checksum manifest contains an invalid digest' || return
  actual="$(sha256_file "$archive")" || return
  actual="$(printf '%s\n' "$actual" | tr '[:upper:]' '[:lower:]')"
  expected="$(printf '%s\n' "$expected" | tr '[:upper:]' '[:lower:]')"
  [[ "$actual" == "$expected" ]] || die 'archive checksum verification failed'
}

validate_cli_archive() {
  local archive="$1"
  local archive_base="$2"
  local extract_root="$3"
  local member
  local listing_line
  local directory_count=0
  local binary_count=0
  local member_count=0
  local members
  local listing

  members="$(LC_ALL=C tar -tzf "$archive")" || die 'could not list CLI archive entries' || return
  while IFS= read -r member; do
    member_count=$((member_count + 1))
    case "$member" in
    "$archive_base/") directory_count=$((directory_count + 1)) ;;
    "$archive_base/confighub") binary_count=$((binary_count + 1)) ;;
    *) die "CLI archive contains an unsafe entry: $member" || return ;;
    esac
  done <<<"$members"
  [[ "$member_count" -eq 2 ]] || die 'CLI archive contains an unexpected number of entries' || return
  [[ "$directory_count" -eq 1 && "$binary_count" -eq 1 ]] ||
    die 'CLI archive entries are missing or duplicated' || return

  listing="$(LC_ALL=C tar -tvzf "$archive")" || die 'could not inspect CLI archive entries' || return
  while IFS= read -r listing_line; do
    case "${listing_line:0:1}" in
    d | -) ;;
    *) die 'CLI archive contains a link or special file' || return ;;
    esac
  done <<<"$listing"

  mkdir -p -- "$extract_root/$archive_base"
  tar -xzf "$archive" -C "$extract_root" "$archive_base/confighub"
  [[ -f "$extract_root/$archive_base/confighub" && ! -L "$extract_root/$archive_base/confighub" ]] ||
    die 'extracted CLI is not a regular file' || return
  chmod 0755 "$extract_root/$archive_base/confighub"
}

remove_download_origin_mark() {
  local operating_system="$1"
  local file="$2"
  local attributes
  local attribute

  case "$operating_system" in
  linux) return 0 ;;
  darwin)
    require_command xattr || return
    attributes="$(xattr "$file")" || die 'could not inspect downloaded CLI attributes' || return
    while IFS= read -r attribute; do
      if [[ "$attribute" == 'com.apple.quarantine' ]]; then
        xattr -d com.apple.quarantine "$file" || die 'could not remove CLI quarantine attribute'
        return
      fi
    done <<<"$attributes"
    ;;
  *) die "unsupported origin-mark platform: $operating_system" ;;
  esac
}

verify_cli_version() {
  local binary="$1"
  local expected="$2"
  local actual

  if ! actual="$("$binary" version 2>/dev/null)"; then
    die 'downloaded CLI could not report its version' || return
  fi
  [[ "$actual" == "$expected" ]] || die "downloaded CLI reported unexpected version: $actual"
}

cleanup_cli_install() {
  if [[ -n "${cli_stage_path:-}" && -e "$cli_stage_path" ]]; then
    unlink -- "$cli_stage_path"
  fi
  if [[ -n "${cli_temp_root:-}" && -d "$cli_temp_root" ]]; then
    rm -rf -- "$cli_temp_root"
  fi
}

install_cli_release() {
  local tag="$1"
  local destination="$2"
  local target
  local operating_system
  local architecture
  local version
  local archive_base
  local archive_name
  local release_url
  local archive_path
  local manifest_path
  local extracted_binary

  validate_release_tag "$tag" || return
  [[ "$destination" == /* ]] || die 'install directory must be an absolute path' || return
  target="$(detect_cli_target "$(uname -s)" "$(uname -m)")" || return
  read -r operating_system architecture <<<"$target"
  version="${tag#v}"
  archive_base="config-hub-cli_${version}_${operating_system}_${architecture}"
  archive_name="$archive_base.tar.gz"
  release_url="$github_web_root/releases/download/$tag"

  cli_temp_root="$(mktemp -d "${TMPDIR:-/tmp}/confighub-cli-install.XXXXXX")"
  trap cleanup_cli_install EXIT INT TERM
  archive_path="$cli_temp_root/$archive_name"
  manifest_path="$cli_temp_root/checksums.txt"
  download_file "$release_url/$archive_name" "$archive_path"
  download_file "$release_url/checksums.txt" "$manifest_path"
  verify_download_checksum "$manifest_path" "$archive_path" "$archive_name" || return
  validate_cli_archive "$archive_path" "$archive_base" "$cli_temp_root/extracted" || return

  extracted_binary="$cli_temp_root/extracted/$archive_base/confighub"
  remove_download_origin_mark "$operating_system" "$extracted_binary" || return
  verify_cli_version "$extracted_binary" "$tag" || return

  mkdir -p -- "$destination"
  [[ ! -e "$destination/confighub" || ( -f "$destination/confighub" && ! -L "$destination/confighub" ) ]] ||
    die 'existing CLI target is not a regular file' || return
  cli_stage_path="$destination/.confighub.install.$$"
  [[ ! -e "$cli_stage_path" ]] || die 'temporary CLI target already exists' || return
  install -m 0755 -- "$extracted_binary" "$cli_stage_path"
  verify_cli_version "$cli_stage_path" "$tag" || return
  mv -f -- "$cli_stage_path" "$destination/confighub"
  cli_stage_path=''
  verify_cli_version "$destination/confighub" "$tag" || return

  cleanup_cli_install
  cli_temp_root=''
  trap - EXIT INT TERM
  printf 'Installed ConfigHub CLI %s to %s/confighub\n' "$tag" "$destination"
}

parse_cli_args() {
  requested_version=''
  install_dir='/usr/local/bin'
  show_help=0

  while [[ "$#" -gt 0 ]]; do
    case "$1" in
    --version)
      [[ "$#" -ge 2 ]] || die '--version requires a value' || return
      requested_version="$2"
      shift 2
      ;;
    --install-dir)
      [[ "$#" -ge 2 ]] || die '--install-dir requires a value' || return
      install_dir="$2"
      shift 2
      ;;
    --help | -h)
      show_help=1
      shift
      ;;
    *) die "unknown argument: $1" || return ;;
    esac
  done

  [[ "$install_dir" == /* ]] || die 'install directory must be an absolute path' || return
  if [[ -n "$requested_version" ]]; then
    validate_release_tag "$requested_version" || return
  fi
}

main() {
  local command_name
  local tag

  parse_cli_args "$@" || return 2
  if [[ "$show_help" -eq 1 ]]; then
    usage
    return 0
  fi
  for command_name in curl tar awk tr mktemp install; do
    require_command "$command_name" || return
  done
  detect_cli_target "$(uname -s)" "$(uname -m)" >/dev/null || return
  if [[ -n "$requested_version" ]]; then
    tag="$requested_version"
  else
    tag="$(resolve_latest_tag)" || return
  fi
  install_cli_release "$tag" "$install_dir"
}

if [[ -z "${BASH_SOURCE[0]:-}" || "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
