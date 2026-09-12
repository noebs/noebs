#!/usr/bin/env bash
set -euo pipefail

: "${EXE_TERRAFORM_DIR:?set the persistent Terraform state directory}"
: "${EXE_SSH_KEY:?set the registered exe.dev SSH identity path}"
repo=$(cd -- "$(dirname -- "$0")/../.." && pwd)
state=$(realpath -m -- "$EXE_TERRAFORM_DIR")
install -d -m 0700 "$state"
platform="$(go env GOOS)_$(go env GOARCH)"
provider_dir="$state/providers/registry.terraform.io/noebs/exedev/0.1.0/$platform"
mkdir -p "$provider_dir"
(cd "$repo/foundation/exedev-provider" && go build -trimpath -o "$provider_dir/terraform-provider-exedev_v0.1.0" .)
cp "$repo/foundation/exedev/main.tf" "$state/main.tf"
cat > "$state/terraform.rc" <<EOF
provider_installation {
  filesystem_mirror {
    path = "$state/providers"
    include = ["registry.terraform.io/noebs/exedev"]
  }
}
EOF
export TF_CLI_CONFIG_FILE="$state/terraform.rc"
export EXEDEV_TOKEN
EXEDEV_TOKEN=$(python3 "$repo/scripts/exe/api-token.py" "$EXE_SSH_KEY")
# Rebuilding the in-repository provider changes its checksum; install this exact build.
rm -f "$state/.terraform.lock.hcl"
terraform -chdir="$state" init -input=false -no-color >/dev/null
terraform -chdir="$state" "$@"
