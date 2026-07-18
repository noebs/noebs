# Alpha image release

`scripts/publish-alpha-image.sh` publishes one reviewed Git commit without relying
on GitHub Actions. It accepts only a full commit SHA, exports that commit with
`git archive`, and builds the export in a private temporary directory. Modified
and untracked working-tree files therefore cannot enter the image.

The command pushes only `ghcr.io/noebs/noebs:<full-source-sha>`. It treats that
tag as write-once and refuses to replace it when it already exists. The image
also carries the source SHA as its OCI revision label. Repository-side immutable
tag protection should remain enabled when the registry offers it; the preflight
check cannot make two concurrent publishers atomic by itself.

## Prerequisites

Run the command on a trusted Linux Docker host with Bash, Git, tar, jq,
`sha256sum`, Docker Engine, and Docker Buildx. The alpha host was checked with
Docker Engine 29.5.2 and Buildx 0.34.0.

Authenticate Docker to GHCR before the release session with an account that can
write `ghcr.io/noebs/noebs`. The script reads the existing
`${DOCKER_CONFIG:-$HOME/.docker}/config.json`; it does not accept credentials,
invoke `docker login`, or print the config. A credential helper or credential
store referenced by that file is supported.

The current alpha host does not have a readable Docker config in the `adonese`
login environment. Supplying that pre-authenticated config is a required manual
release prerequisite; do not put a token in this repository or on the command
line.

## Publish

Choose the exact reviewed commit and a new receipt path:

```bash
source_sha=$(git rev-parse --verify HEAD^{commit})
scripts/publish-alpha-image.sh \
  "$source_sha" \
  "$HOME/noebs-release-$source_sha.json"
```

The builder uses the `docker-container` driver. Its aggregate BuildKit cgroup is
limited to 2 GiB memory with no additional swap, two CPUs, and 512 PIDs. The same
memory and CPU limits are applied to individual build steps. The script verifies
the effective container limits before starting the build, requests current base
images, and emits maximum BuildKit provenance.

After the push, the script reads the tag back from GHCR as raw manifest bytes,
hashes those bytes independently, and requires that digest to equal the digest
in Buildx's metadata. Only then does it create the receipt. A receipt contains
the source commit and tree, immutable tag, verified digest, digest reference, and
platform; it contains no credential material.

The dedicated builder, BuildKit container and state volume, build context, and
metadata are removed on success and on failure. Cleanup targets only names made
for that invocation. The verified registry image and the requested receipt are
the intended persistent outputs.

## Promote by digest

This command never edits Kubernetes manifests and never deploys. Review the
receipt, then make a separate GitOps commit that replaces the Noebs digest in
the current-host overlay with the receipt's `digest`. Render and validate that
commit before allowing Argo CD to reconcile. The coordinated migration and
rollback boundary remains documented in the
[current-host release notes](../deploy/kubernetes/overlays/current-host/README.md).

If the command fails after the push, inspect the full-SHA tag and retained
receipt before doing anything else. Do not overwrite or delete the tag to make a
retry succeed; resolve the release evidence or publish a new reviewed commit.
