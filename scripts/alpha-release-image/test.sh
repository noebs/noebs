#!/usr/bin/env bash
set -euo pipefail

test_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
script="$test_dir/../publish-alpha-image.sh"
dockerfile="$test_dir/../../Dockerfile"

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

assert_dockerfile_contains() {
  grep -Fq -- "$1" "$dockerfile" || fail "missing container supply-chain invariant: $1"
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

[[ $(grep -c '^FROM ' "$dockerfile") -eq 2 ]] || fail "Dockerfile must have exactly two stages"
[[ $(grep -Ec '^FROM [^ ]+@sha256:[0-9a-f]{64}( AS [a-z]+)?$' "$dockerfile") -eq 2 ]] || \
  fail "every container stage must use an immutable manifest digest"
assert_dockerfile_contains 'golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651'
assert_dockerfile_contains 'debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818'
assert_dockerfile_contains '5488e32bc471de7982ad895dd054bbab3ab91c417a118426134551e9626e4e85  /tmp/sops'
assert_dockerfile_contains 'age-v1.2.1-linux-amd64.tar.gz'
assert_dockerfile_contains '7df45a6cc87d4da11cc03a539a7470c15b1041ab2b396af088fe9990f7c79d50  /tmp/age.tar.gz'
assert_dockerfile_contains "sha256sum -c -"
[[ $(grep -nF '5488e32bc471de7982ad895dd054bbab3ab91c417a118426134551e9626e4e85  /tmp/sops' "$dockerfile" | cut -d: -f1) -lt \
   $(grep -nF 'install -m 0755 /tmp/sops' "$dockerfile" | cut -d: -f1) ]] || \
  fail "SOPS must be verified before installation"
[[ $(grep -nF '7df45a6cc87d4da11cc03a539a7470c15b1041ab2b396af088fe9990f7c79d50  /tmp/age.tar.gz' "$dockerfile" | cut -d: -f1) -lt \
   $(grep -nF 'tar --extract --gzip --file /tmp/age.tar.gz' "$dockerfile" | cut -d: -f1) ]] || \
  fail "age must be verified before extraction"
if grep -Eq 'age-v1\.2\.0|(^|[[:space:]])wget([[:space:]]|$)' "$dockerfile"; then
  fail "known-vulnerable age or unchecked downloader remains in Dockerfile"
fi

printf 'PASS: alpha release image workflow invariants\n'
