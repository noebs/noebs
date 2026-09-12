#!/usr/bin/env bash
set -euo pipefail
target=${1:?set binary output directory}
mkdir -p "$target"
archive=$(mktemp)
trap 'rm -f "$archive"' EXIT
curl -fsSL https://releases.hashicorp.com/terraform/1.16.2/terraform_1.16.2_linux_amd64.zip -o "$archive"
echo "0d17011f0c4664539b164b044903d04e296c86c13cb9f28040076c65cfb3985a  $archive" | sha256sum -c - >/dev/null
unzip -p "$archive" terraform > "$target/terraform"
chmod 0755 "$target/terraform"
