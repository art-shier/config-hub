#!/usr/bin/env bash
set -euo pipefail

umask 022

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
release_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
release_temp_root=''

die() {
  printf 'package-release: %s\n' "$1" >&2
  return 1
}

validate_release_tag() {
  local tag="${1:-}"
  [[ "$tag" =~ $release_tag_pattern ]] || die 'version must match vMAJOR.MINOR.PATCH'
}

release_version() {
  local tag="${1:-}"
  validate_release_tag "$tag" || return
  printf '%s\n' "${tag#v}"
}

validate_release_target() {
  local product="${1:-}"
  local operating_system="${2:-}"
  local architecture="${3:-}"

  case "$product/$operating_system/$architecture" in
  server/linux/amd64 | server/linux/arm64 | \
    cli/linux/amd64 | cli/linux/arm64 | \
    cli/darwin/arm64 | cli/windows/amd64) ;;
  *) die "unsupported release target: $product/$operating_system/$architecture" ;;
  esac
}

release_binary_name() {
  local product="${1:-}"
  local operating_system="${2:-}"

  case "$product/$operating_system" in
  server/linux) printf '%s\n' 'confighub-server' ;;
  cli/linux | cli/darwin) printf '%s\n' 'confighub' ;;
  cli/windows) printf '%s\n' 'confighub.exe' ;;
  *) die "unsupported release binary: $product/$operating_system" ;;
  esac
}

release_archive_base() {
  local product="${1:-}"
  local tag="${2:-}"
  local operating_system="${3:-}"
  local architecture="${4:-}"
  local version

  validate_release_target "$product" "$operating_system" "$architecture" || return
  version="$(release_version "$tag")" || return
  printf 'config-hub-%s_%s_%s_%s\n' \
    "$product" "$version" "$operating_system" "$architecture"
}

release_archive_name() {
  local product="${1:-}"
  local tag="${2:-}"
  local operating_system="${3:-}"
  local architecture="${4:-}"
  local archive_base

  archive_base="$(release_archive_base "$product" "$tag" "$operating_system" "$architecture")" || return
  case "$product/$operating_system/$architecture" in
  cli/windows/amd64) printf '%s.zip\n' "$archive_base" ;;
  *) printf '%s.tar.gz\n' "$archive_base" ;;
  esac
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

verify_release_source() {
  local tag="${1:-}"
  local status

  [[ "$(uname -s)" == 'Linux' ]] || die 'release packaging requires Linux'
  validate_release_tag "$tag" || return
  git -C "$repo_root" tag --points-at HEAD | grep -Fx -- "$tag" >/dev/null ||
    die "tag $tag does not point at HEAD"
  status="$(git -C "$repo_root" status --porcelain)"
  [[ -z "$status" ]] || die 'release packaging requires a clean worktree'

  for command_name in git npm go tar gzip zip sha256sum install mktemp; do
    require_command "$command_name" || return
  done
  tar --version | grep -F 'GNU tar' >/dev/null || die 'release packaging requires GNU tar'
}

create_archive() {
  local package_root="$1"
  local archive_base="$2"
  local output_path="$3"
  local source_epoch="$4"

  (
    cd "$package_root"
    LC_ALL=C tar --sort=name --mtime="@$source_epoch" --owner=0 --group=0 --numeric-owner \
      -cf - -- "$archive_base" | gzip -n >"$output_path"
  )
}

create_zip_archive() {
  local package_root="$1"
  local archive_base="$2"
  local output_path="$3"
  local source_epoch="$4"

  (
    cd "$package_root"
    TZ=UTC touch -d "@$source_epoch" -- "$archive_base" "$archive_base/confighub.exe"
    TZ=UTC LC_ALL=C zip -X -q "$output_path" "$archive_base/" "$archive_base/confighub.exe"
  )
}

cleanup_release_temp() {
  if [[ -n "${release_temp_root:-}" && -d "$release_temp_root" ]]; then
    rm -rf -- "$release_temp_root"
  fi
}

build_release() {
  local tag="$1"
  local source_epoch
  local temp_root
  local package_root
  local asset_root
  local archive_count
  local release_dir="$repo_root/dist/release"

  verify_release_source "$tag" || return
  "$script_dir/verify-toolchain.sh"

  cd "$repo_root"
  npm ci --include=dev --prefix "$repo_root/web"
  npm run typecheck --prefix "$repo_root/web"
  npm test --prefix "$repo_root/web"
  npm run build --prefix "$repo_root/web"
  go test ./...

  source_epoch="$(git -C "$repo_root" show -s --format=%ct HEAD)"
  [[ "$source_epoch" =~ ^[0-9]+$ ]] || die 'source commit timestamp is invalid'

  temp_root="$(mktemp -d "${TMPDIR:-/tmp}/confighub-release.XXXXXX")"
  release_temp_root="$temp_root"
  trap cleanup_release_temp EXIT INT TERM
  package_root="$temp_root/packages"
  asset_root="$temp_root/assets"
  mkdir -p -- "$package_root" "$asset_root"

  local architecture
  for architecture in amd64 arm64; do
    local server_base
    local server_archive_name
    local server_binary
    server_base="$(release_archive_base server "$tag" linux "$architecture")"
    server_archive_name="$(release_archive_name server "$tag" linux "$architecture")"
    server_binary="$(release_binary_name server linux)"

    mkdir -p -- "$package_root/$server_base/config" "$package_root/$server_base/deploy"
    CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go build -trimpath -buildvcs=false \
      -ldflags "-X confighub.local/internal/buildinfo.Version=$tag" \
      -o "$package_root/$server_base/$server_binary" ./cmd/server

    chmod 0755 -- "$package_root/$server_base/$server_binary"
    install -m 0644 -- "$repo_root/config/config.example.yaml" \
      "$package_root/$server_base/config/config.example.yaml"
    install -m 0644 -- "$repo_root/config/users.example.yaml" \
      "$package_root/$server_base/config/users.example.yaml"
    install -m 0644 -- "$repo_root/deploy/systemd/confighub.service" \
      "$package_root/$server_base/deploy/confighub.service"

    create_archive "$package_root" "$server_base" \
      "$asset_root/$server_archive_name" "$source_epoch"
  done

  local target
  local operating_system
  for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
    local cli_base
    local cli_archive_name
    local cli_binary
    IFS=/ read -r operating_system architecture <<<"$target"
    cli_base="$(release_archive_base cli "$tag" "$operating_system" "$architecture")"
    cli_archive_name="$(release_archive_name cli "$tag" "$operating_system" "$architecture")"
    cli_binary="$(release_binary_name cli "$operating_system")"

    mkdir -p -- "$package_root/$cli_base"
    CGO_ENABLED=0 GOOS="$operating_system" GOARCH="$architecture" \
      go build -trimpath -buildvcs=false \
      -ldflags "-X confighub.local/internal/buildinfo.Version=$tag" \
      -o "$package_root/$cli_base/$cli_binary" ./cmd/cli
    chmod 0755 -- "$package_root/$cli_base/$cli_binary"

    if [[ "$operating_system" == 'windows' ]]; then
      create_zip_archive "$package_root" "$cli_base" \
        "$asset_root/$cli_archive_name" "$source_epoch"
    else
      create_archive "$package_root" "$cli_base" \
        "$asset_root/$cli_archive_name" "$source_epoch"
    fi
  done

  (
    cd "$asset_root"
    LC_ALL=C sha256sum config-hub-*.tar.gz config-hub-*.zip | LC_ALL=C sort -k2 >checksums.txt
  )
  archive_count="$(find "$asset_root" -maxdepth 1 -type f \
    \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l)"
  [[ "$archive_count" -eq 6 ]] || die 'release packaging did not create six archives'

  if [[ -z "$repo_root" || "$repo_root" == '/' || "$release_dir" != "$repo_root/dist/release" ]]; then
    die 'invalid release output path'
  fi
  mkdir -p -- "$repo_root/dist"
  rm -rf -- "$release_dir"
  mv -- "$asset_root" "$release_dir"
  cleanup_release_temp
  release_temp_root=''
  trap - EXIT INT TERM
  printf 'Release assets written to %s\n' "$release_dir"
}

main() {
  if [[ "$#" -ne 1 ]]; then
    printf '%s\n' 'Usage: package-release.sh vMAJOR.MINOR.PATCH' >&2
    return 2
  fi
  build_release "$1"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
