# Alpha image release

`scripts/publish-alpha-image.sh` publishes one reviewed Git commit. CI uses the same publisher with a job-scoped registry token. It accepts only a full commit SHA, exports that commit with
`git archive`, and builds the export in a private temporary directory. Modified
and untracked working-tree files therefore cannot enter the image.

The command pushes only `ghcr.io/noebs/noebs:<full-source-sha>`. It treats that
tag as write-once and refuses to replace it when it already exists. The image
also carries the source SHA as its OCI revision label. Repository-side immutable
tag protection should remain enabled when the registry offers it; the preflight
check cannot make two concurrent publishers atomic by itself.

## Prerequisites

Run the command on a trusted Linux Docker host with Bash, Git, tar, jq,
`sha256sum`, Docker Engine, and Docker Buildx.

Authenticate Docker to GHCR before the release session with an account that can
write `ghcr.io/noebs/noebs`. The script reads the existing
`${DOCKER_CONFIG:-$HOME/.docker}/config.json`; it does not accept credentials,
invoke `docker login`, or print the config. A credential helper or credential
store referenced by that file is supported.

For manual publication, supply a pre-authenticated Docker config; do not put a token in this repository or on the command line. CI authenticates only for its job and removes the credentials afterward.

The Dockerfile pins both Docker Hub base manifests by digest. The runtime image
does not contain SOPS, age, an age identity, or a SOPS working directory. Secret
decryption is a release-host responsibility, and containers receive only the
service-specific plaintext files they need.

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
limits bound the BuildKit daemon and all work it launches. The script verifies
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

The publisher produces an image and receipt. `infra/deploy` consumes the
application receipt, verifies its source SHA and registry digest, prepares
service secrets, then runs bootstrap, migrations, and workload rollout in
dependency order. The image checker compares every Noebs application and init
container with the receipt, including cleanup CronJobs. See the
[deployment guide](../infra/README.md) for the complete entrypoint.

The checked-in digest pins in `infra/kubernetes/overlays/exe`,
`infra/kubernetes/bootstrap`, and the lookup and membership operation overlays
must remain coherent when those manifests are used directly. Render each
workflow and verify its Noebs image against the selected receipt.

If the command fails after the push, inspect the full-SHA tag and retained
receipt before retrying. Do not overwrite or delete the tag to make a retry
succeed; resolve the release evidence or publish a new reviewed commit.

Mojaloop and its SDK have an independent release lifecycle. The Noebs fleet
release requires only its application image receipt and never publishes or
deploys an external switch or SDK.
