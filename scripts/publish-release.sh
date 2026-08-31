#!/usr/bin/env bash
set -euo pipefail

github_repository='art-shier/config-hub'
release_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
owned_draft=0
owned_tag=''

die() {
  printf 'publish-release: %s\n' "$1" >&2
  return 1
}

validate_release_tag() {
  local tag="${1:-}"
  [[ "$tag" =~ $release_tag_pattern ]] || die 'version must match vMAJOR.MINOR.PATCH'
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

cleanup_owned_draft() {
  local status="$?"
  local draft_state=''
  trap - EXIT
  if [[ "$owned_draft" -eq 1 && -n "$owned_tag" ]]; then
    draft_state="$(gh release view "$owned_tag" --repo "$github_repository" \
      --json isDraft --jq '.isDraft' 2>/dev/null || true)"
    if [[ "$draft_state" == true ]]; then
      gh release delete "$owned_tag" --repo "$github_repository" --yes >/dev/null 2>&1 || true
    fi
  fi
  exit "$status"
}

expected_asset_names() {
  local tag="$1"
  local version="${tag#v}"
  printf '%s\n' \
    checksums.txt \
    "config-hub-cli_${version}_linux_amd64.tar.gz" \
    "config-hub-cli_${version}_linux_arm64.tar.gz" \
    "config-hub-server_${version}_linux_amd64.tar.gz" \
    "config-hub-server_${version}_linux_arm64.tar.gz"
}

validate_local_assets() {
  local tag="$1"
  local release_dir="$2"
  local expected
  local actual
  local expected_archives
  local manifest_archives

  [[ -d "$release_dir" && ! -L "$release_dir" ]] || die 'release directory must be a regular directory' || return
  expected="$(expected_asset_names "$tag")"
  actual="$(find "$release_dir" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)"
  [[ "$actual" == "$expected" ]] || die 'release directory does not contain the exact five assets' || return

  expected_archives="$(printf '%s\n' "$expected" | awk '$0 != "checksums.txt"')"
  manifest_archives="$(awk 'NF == 2 { print $2 }' "$release_dir/checksums.txt" | LC_ALL=C sort)"
  [[ "$manifest_archives" == "$expected_archives" ]] ||
    die 'checksum manifest does not cover the exact four archives' || return
  (
    cd "$release_dir"
    sha256sum --check --strict checksums.txt >/dev/null
  ) || die 'local Release asset checksum verification failed'
}

publish_release() {
  local tag="$1"
  local release_dir="$2"
  local expected
  local remote_assets
  local remote_draft
  local command_name
  local -a asset_paths=()
  local asset_name

  validate_release_tag "$tag" || return
  require_command gh || return
  for command_name in find awk sort sha256sum; do
    require_command "$command_name" || return
  done
  release_dir="$(cd -- "$release_dir" && pwd -P)" || die 'could not resolve release directory' || return
  validate_local_assets "$tag" "$release_dir" || return

  if gh release view "$tag" --repo "$github_repository" >/dev/null 2>&1; then
    die "Release $tag already exists and will not be overwritten" || return
  fi

  expected="$(expected_asset_names "$tag")"
  while IFS= read -r asset_name; do
    asset_paths+=("$release_dir/$asset_name")
  done <<<"$expected"

  gh release create "$tag" --repo "$github_repository" --draft --verify-tag \
    --generate-notes --title "$tag"
  owned_draft=1
  owned_tag="$tag"
  trap cleanup_owned_draft EXIT

  gh release upload "$tag" "${asset_paths[@]}" --repo "$github_repository"
  remote_draft="$(gh release view "$tag" --repo "$github_repository" \
    --json isDraft --jq '.isDraft')"
  [[ "$remote_draft" == true ]] || die 'Release unexpectedly left draft state before verification' || return
  remote_assets="$(gh release view "$tag" --repo "$github_repository" \
    --json assets --jq '.assets[].name' | LC_ALL=C sort)"
  [[ "$remote_assets" == "$expected" ]] || die 'remote Release assets do not match the local manifest' || return

  gh release edit "$tag" --repo "$github_repository" --draft=false
  owned_draft=0
  owned_tag=''
  trap - EXIT
  printf 'Published ConfigHub Release %s with five verified assets.\n' "$tag"
}

main() {
  if [[ "$#" -ne 2 ]]; then
    printf '%s\n' 'Usage: publish-release.sh vMAJOR.MINOR.PATCH RELEASE_DIRECTORY' >&2
    return 2
  fi
  publish_release "$1" "$2"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
