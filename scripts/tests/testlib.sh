#!/usr/bin/env bash

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  local expected="$1"
  local actual="$2"
  if [[ "$actual" != "$expected" ]]; then
    fail "expected [$expected], got [$actual]"
  fi
}

assert_file() {
  [[ -f "$1" ]] || fail "expected regular file: $1"
}

assert_not_file() {
  [[ ! -f "$1" ]] || fail "unexpected regular file: $1"
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  [[ "$haystack" == *"$needle"* ]] || fail "expected [$haystack] to contain [$needle]"
}

assert_fails() {
  if "$@"; then
    fail "expected command to fail: $*"
  fi
}
