#!/usr/bin/env bash
set -eu

count=0

add() {
  count=$((count + 1))
}

for name in "$@"; do
  add
  printf '%s\n' "$name"
done

printf 'count=%s\n' "$count"
