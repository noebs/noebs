#!/usr/bin/env bash
set -euo pipefail

test_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
script="$test_dir/../publish-alpha-image.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  grep -Fq -- "$1" "$script" || fail "missing release invariant: $1"
}

assert_absent() {
  if grep -Fq -- "$1" "$script"; then
    fail "forbidden release behavior is present: $1"
  fi
}

bash -n "$script"

help=$($script --help)
[[ $help == *'<40-character-source-sha>'* ]] || fail "help does not require a full source SHA"
[[ $help == *'write-once'* ]] || fail "help does not describe immutable tag behavior"

if "$script" deadbeef /tmp/noebs-invalid-release.json >/dev/null 2>&1; then
  fail "short source SHA was accepted"
fi

assert_contains 'git -C "$repo_root" archive --format=tar "$source_sha"'
assert_contains '--driver docker-container'
assert_contains '--driver-opt "memory=$memory_limit"'
assert_contains '--driver-opt "cpu-quota=$cpu_quota"'
assert_contains 'container update --pids-limit "$pids_limit"'
assert_contains '.PidsLimit == $pids'
assert_contains '--tag "$tag_ref"'
assert_contains '--metadata-file "$metadata_path"'
assert_contains '--push'
assert_contains 'imagetools inspect --raw "$tag_ref"'
assert_contains 'sha256sum "$registry_manifest"'
assert_contains '[[ $registry_digest == "$build_digest" ]]'
assert_contains 'generated builder resources are already in use'
assert_contains 'buildx rm --force "$builder_name"'
assert_contains 'volume rm "$builder_volume"'
assert_contains 'Docker config does not reference GHCR credentials'

assert_absent 'docker login'
assert_absent ':master'
assert_absent '--load'
assert_absent '--keep-state'
assert_absent '--resource'
assert_absent 'set -x'

printf 'PASS: alpha release image workflow invariants\n'
