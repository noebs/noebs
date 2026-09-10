#!/usr/bin/env bash
set -Eeuo pipefail
[[ $# == 2 && $1 =~ ^[0-9a-f]{40}$ && ! -e $2 ]]
source_sha=$1
receipt=$2
[[ $(git rev-parse HEAD) == "$source_sha" ]]
tag="ghcr.io/noebs/noebs:mojaloop-sdk-$source_sha"
build_name="noebs-sdk-${source_sha:0:12}-$$"
build_dir=$(mktemp -d)
cleanup() {
  docker buildx rm --force "$build_name" >/dev/null 2>&1 || true
  rm -rf -- "$build_dir"
}
trap cleanup EXIT
if docker buildx imagetools inspect "$tag" > /dev/null 2>"$build_dir/probe"; then
  echo 'Immutable SDK tag already exists' >&2; exit 1
fi
grep -Eiq 'manifest unknown|not found' "$build_dir/probe"
git archive "$source_sha" deploy/mojaloop-sdk | tar -xf - -C "$build_dir"
docker buildx create --name "$build_name" --driver docker-container \
  --driver-opt memory=1g --driver-opt memory-swap=1g \
  --driver-opt cpu-period=100000 --driver-opt cpu-quota=200000 --driver-opt restart-policy=no >/dev/null
docker buildx inspect "$build_name" --bootstrap >/dev/null
docker update --pids-limit 512 "buildx_buildkit_${build_name}0" >/dev/null
docker buildx build --builder "$build_name" --platform linux/amd64 --pull --provenance mode=max \
  --label "org.opencontainers.image.revision=$source_sha" \
  --label org.opencontainers.image.source=https://github.com/noebs/noebs \
  --metadata-file "$build_dir/metadata.json" --tag "$tag" --push "$build_dir/deploy/mojaloop-sdk"
digest=$(jq -er '."containerimage.digest"' "$build_dir/metadata.json")
docker buildx imagetools inspect --raw "$tag" > "$build_dir/manifest.json"
[[ "sha256:$(sha256sum "$build_dir/manifest.json" | cut -d' ' -f1)" == "$digest" ]]
jq -n --arg source "$source_sha" --arg tag "$tag" --arg digest "$digest" \
  '{source_sha:$source,tag:$tag,digest:$digest,digest_ref:("ghcr.io/noebs/noebs@"+$digest),profile:"sdg-msisdn-v1",upstream:"6594dc5689a95dffece23d481a407e4368d80a96"}' > "$receipt"
