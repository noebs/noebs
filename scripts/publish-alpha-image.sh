#!/usr/bin/env bash
set -Eeuo pipefail

readonly repository="ghcr.io/noebs/noebs"
readonly platform="linux/amd64"
readonly memory_limit="2g"
readonly memory_bytes=2147483648
readonly cpu_period=100000
readonly cpu_quota=200000
readonly pids_limit=512

usage() {
  cat <<'EOF'
Usage: scripts/publish-alpha-image.sh <40-character-source-sha> <receipt.json>

Build and publish one clean Git commit as ghcr.io/noebs/noebs:<source-sha>.
The destination tag is write-once: the command refuses to replace an existing tag.
EOF
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

if [[ ${1:-} == "--help" || ${1:-} == "-h" ]]; then
  usage
  exit 0
fi

[[ $# -eq 2 ]] || {
  usage >&2
  exit 2
}

source_sha=$1
receipt_argument=$2

[[ $source_sha =~ ^[0-9a-f]{40}$ ]] || fail "source SHA must be exactly 40 lowercase hexadecimal characters"
[[ ! -e $receipt_argument && ! -L $receipt_argument ]] || fail "receipt already exists: $receipt_argument"

for command in docker git grep jq mktemp sha256sum tar; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel 2>/dev/null) || fail "script is not inside a Git worktree"
resolved_sha=$(git -C "$repo_root" rev-parse --verify "${source_sha}^{commit}" 2>/dev/null) || fail "source commit is unavailable locally: $source_sha"
[[ $resolved_sha == "$source_sha" ]] || fail "source SHA does not identify the commit object directly"
source_tree=$(git -C "$repo_root" rev-parse --verify "${source_sha}^{tree}")

receipt_dir=$(CDPATH= cd -- "$(dirname -- "$receipt_argument")" 2>/dev/null && pwd) || fail "receipt directory does not exist"
receipt_path="$receipt_dir/$(basename -- "$receipt_argument")"
[[ ! -e $receipt_path && ! -L $receipt_path ]] || fail "receipt already exists: $receipt_path"

docker_config=${DOCKER_CONFIG:-${HOME:?HOME is required}/.docker}
docker_config_file="$docker_config/config.json"
[[ -r $docker_config_file ]] || fail "a readable Docker config is required"
jq -e --arg registry "ghcr.io" '
  ((.auths // {}) | has($registry))
  or ((.credHelpers // {}) | has($registry))
  or (
    ((.credsStore // "") | type) == "string"
    and ((.credsStore // "") | length) > 0
  )
' "$docker_config_file" >/dev/null 2>&1 || fail "Docker config does not reference GHCR credentials"

docker info >/dev/null 2>&1 || fail "Docker Engine is unavailable"
docker buildx version >/dev/null 2>&1 || fail "Docker Buildx is unavailable"

umask 077
temp_root=$(mktemp -d "${TMPDIR:-/tmp}/noebs-release.XXXXXXXX")
receipt_temp=$(mktemp "$receipt_dir/.noebs-release-receipt.XXXXXXXX")
builder_name="noebs-release-${source_sha:0:12}-$$-$RANDOM"
builder_container="buildx_buildkit_${builder_name}0"
builder_volume="${builder_container}_state"
builder_owned=0

remove_builder() {
  local cleanup_failed=0

  docker buildx rm --force "$builder_name" >/dev/null 2>&1 || cleanup_failed=1

  if docker container inspect "$builder_container" >/dev/null 2>&1; then
    docker container rm --force "$builder_container" >/dev/null 2>&1 || cleanup_failed=1
  fi
  if docker volume inspect "$builder_volume" >/dev/null 2>&1; then
    docker volume rm "$builder_volume" >/dev/null 2>&1 || cleanup_failed=1
  fi

  return "$cleanup_failed"
}

cleanup() {
  local status=$?
  local cleanup_failed=0
  trap - EXIT
  set +e

  if ((builder_owned)); then
    remove_builder || cleanup_failed=1
  fi
  if [[ -n ${temp_root:-} && -e $temp_root ]]; then
    rm -rf -- "$temp_root" || cleanup_failed=1
  fi
  if [[ -n ${receipt_temp:-} && -e $receipt_temp ]]; then
    rm -f -- "$receipt_temp" || cleanup_failed=1
  fi

  if ((status == 0 && cleanup_failed)); then
    printf 'error: release artifacts could not be removed completely\n' >&2
    status=1
  fi
  exit "$status"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

tag_ref="$repository:$source_sha"
tag_probe_error="$temp_root/tag-probe.err"
if docker buildx imagetools inspect --raw "$tag_ref" >/dev/null 2>"$tag_probe_error"; then
  fail "immutable release tag already exists: $tag_ref"
fi
grep -Eiq 'manifest unknown|not found' "$tag_probe_error" || fail "could not prove that the immutable release tag is unused"

context_dir="$temp_root/context"
mkdir -m 0700 "$context_dir"
git -C "$repo_root" archive --format=tar "$source_sha" | tar -xf - -C "$context_dir"
[[ -f $context_dir/Dockerfile ]] || fail "source commit does not contain Dockerfile"

if docker buildx inspect "$builder_name" >/dev/null 2>&1; then
  fail "generated builder name is already in use"
fi
if docker container inspect "$builder_container" >/dev/null 2>&1 \
  || docker volume inspect "$builder_volume" >/dev/null 2>&1; then
  fail "generated builder resources are already in use"
fi
builder_owned=1
docker buildx create \
  --name "$builder_name" \
  --driver docker-container \
  --driver-opt "memory=$memory_limit" \
  --driver-opt "memory-swap=$memory_limit" \
  --driver-opt "cpu-period=$cpu_period" \
  --driver-opt "cpu-quota=$cpu_quota" \
  --driver-opt "restart-policy=no" \
  --driver-opt "provenance-add-gha=false" \
  >/dev/null
docker buildx inspect "$builder_name" --bootstrap >/dev/null

docker container update --pids-limit "$pids_limit" "$builder_container" >/dev/null
builder_resources=$(docker container inspect --format '{{json .HostConfig}}' "$builder_container")
jq -e \
  --argjson memory "$memory_bytes" \
  --argjson cpu_period "$cpu_period" \
  --argjson cpu_quota "$cpu_quota" \
  --argjson pids "$pids_limit" '
    .Memory == $memory
    and .MemorySwap == $memory
    and .CpuPeriod == $cpu_period
    and .CpuQuota == $cpu_quota
    and .PidsLimit == $pids
    and .RestartPolicy.Name == "no"
  ' <<<"$builder_resources" >/dev/null || fail "BuildKit resource limits were not applied exactly"

metadata_path="$temp_root/build-metadata.json"
docker buildx build \
  --builder "$builder_name" \
  --platform "$platform" \
  --pull \
  --provenance mode=max \
  --resource "memory=$memory_limit" \
  --resource "memory-swap=$memory_limit" \
  --resource "cpu-period=$cpu_period" \
  --resource "cpu-quota=$cpu_quota" \
  --label "org.opencontainers.image.revision=$source_sha" \
  --label "org.opencontainers.image.source=https://github.com/noebs/noebs" \
  --tag "$tag_ref" \
  --metadata-file "$metadata_path" \
  --push \
  "$context_dir"

build_digest=$(jq -er '."containerimage.digest" | select(type == "string")' "$metadata_path")
[[ $build_digest =~ ^sha256:[0-9a-f]{64}$ ]] || fail "Buildx returned an invalid image digest"

registry_manifest="$temp_root/registry-manifest.json"
docker buildx imagetools inspect --raw "$tag_ref" >"$registry_manifest"
[[ -s $registry_manifest ]] || fail "registry returned an empty manifest"
read -r registry_hash _ < <(sha256sum "$registry_manifest")
registry_digest="sha256:$registry_hash"
[[ $registry_digest == "$build_digest" ]] || fail "pushed tag does not resolve to the Buildx digest"

jq -n \
  --arg source_sha "$source_sha" \
  --arg source_tree "$source_tree" \
  --arg repository "$repository" \
  --arg tag "$tag_ref" \
  --arg digest "$registry_digest" \
  --arg digest_ref "$repository@$registry_digest" \
  --arg platform "$platform" '
    {
      schema_version: 1,
      source_sha: $source_sha,
      source_tree: $source_tree,
      repository: $repository,
      tag: $tag,
      digest: $digest,
      digest_ref: $digest_ref,
      platform: $platform
    }
  ' >"$receipt_temp"
ln -- "$receipt_temp" "$receipt_path" || fail "receipt appeared while the release was running: $receipt_path"
rm -f -- "$receipt_temp"
receipt_temp=

remove_builder || fail "release builder could not be removed completely"
builder_owned=0
rm -rf -- "$temp_root"
temp_root=

printf 'release_ref=%s\n' "$repository@$registry_digest"
printf 'receipt=%s\n' "$receipt_path"
