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

release_archive_base() {
  local product="${1:-}"
  local tag="${2:-}"
  local arch="${3:-}"
  local version

  case "$product" in
  server | cli) ;;
  *) die 'product must be server or cli' || return ;;
  esac
  case "$arch" in
  amd64 | arm64) ;;
  *) die 'architecture must be amd64 or arm64' || return ;;
  esac
  version="$(release_version "$tag")" || return
  printf 'config-hub-%s_%s_linux_%s\n' "$product" "$version" "$arch"
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

  for command_name in git npm go tar gzip sha256sum install mktemp; do
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

  local arch
  for arch in amd64 arm64; do
    local server_base
    local cli_base
    server_base="$(release_archive_base server "$tag" "$arch")"
    cli_base="$(release_archive_base cli "$tag" "$arch")"

    mkdir -p -- "$package_root/$server_base/config" "$package_root/$server_base/deploy" \
      "$package_root/$cli_base"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false \
      -ldflags "-X confighub.local/internal/buildinfo.Version=$tag" \
      -o "$package_root/$server_base/confighub-server" ./cmd/server
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false \
      -ldflags "-X confighub.local/internal/buildinfo.Version=$tag" \
      -o "$package_root/$cli_base/confighub" ./cmd/cli

    chmod 0755 -- "$package_root/$server_base/confighub-server" "$package_root/$cli_base/confighub"
    install -m 0644 -- "$repo_root/config/config.example.yaml" \
      "$package_root/$server_base/config/config.example.yaml"
    install -m 0644 -- "$repo_root/config/users.example.yaml" \
      "$package_root/$server_base/config/users.example.yaml"
    install -m 0644 -- "$repo_root/deploy/systemd/confighub.service" \
      "$package_root/$server_base/deploy/confighub.service"

    create_archive "$package_root" "$server_base" "$asset_root/$server_base.tar.gz" "$source_epoch"
    create_archive "$package_root" "$cli_base" "$asset_root/$cli_base.tar.gz" "$source_epoch"
  done

  (
    cd "$asset_root"
    LC_ALL=C sha256sum config-hub-*.tar.gz | LC_ALL=C sort -k2 >checksums.txt
  )
  [[ "$(find "$asset_root" -maxdepth 1 -type f -name '*.tar.gz' | wc -l)" -eq 4 ]] ||
    die 'release packaging did not create four archives'

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
