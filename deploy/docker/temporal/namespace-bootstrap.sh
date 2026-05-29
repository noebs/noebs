#!/usr/bin/env bash
set -euo pipefail

temporal_namespace="default"
temporal_address="temporal-frontend:7233"
temporal_retention="72h"
temporal_description="Noebs wallet workflow namespace"
temporal_host="${temporal_address%:*}"
temporal_port="${temporal_address##*:}"
describe_stderr="/tmp/temporal-namespace-describe.err"

until nc -z "$temporal_host" "$temporal_port"; do
  echo "waiting for Temporal frontend at $temporal_address"
  sleep 1
done

if temporal operator namespace describe \
  --address "$temporal_address" \
  --namespace "$temporal_namespace" \
  --disable-config-env \
  --disable-config-file \
  >/dev/null 2>"$describe_stderr"; then
  exit 0
fi

if ! grep -Fq "Namespace $temporal_namespace is not found." "$describe_stderr"; then
  cat "$describe_stderr" >&2
  exit 1
fi

temporal operator namespace create \
  --address "$temporal_address" \
  --namespace "$temporal_namespace" \
  --retention "$temporal_retention" \
  --description "$temporal_description" \
  --disable-config-env \
  --disable-config-file
