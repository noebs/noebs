#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || ("$1" != dry-run && "$1" != apply) ]]; then
  echo "usage: $0 <dry-run|apply>" >&2
  exit 2
fi

mode="$1"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/../../.." && pwd)"
steady_root="$repo_root/deploy/kubernetes/overlays/current-host"
operation_root="$script_dir/memberships/$mode"
authority_root="$repo_root/deploy/kubernetes/keycloak-authority"
git -C "$repo_root" diff --quiet
git -C "$repo_root" diff --cached --quiet
test -z "$(git -C "$repo_root" ls-files --others --exclude-standard)"

command -v jq >/dev/null
command -v python3 >/dev/null
if command -v kubectl >/dev/null 2>&1; then
  kubectl_cmd=(kubectl)
elif command -v k3s >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  kubectl_cmd=(sudo -n k3s kubectl)
else
  echo "kubectl is unavailable" >&2
  exit 1
fi

render() {
  if command -v kustomize >/dev/null 2>&1; then
    kustomize build "$1"
  else
    "${kubectl_cmd[@]}" kustomize "$1"
  fi
}

head_revision="$(git -C "$repo_root" rev-parse --verify HEAD^{commit})"
application="$("${kubectl_cmd[@]}" -n argocd get application noebs -o json)"
jq -e --arg revision "$head_revision" '
  .spec.source.targetRevision == $revision
  and .spec.source.path == "deploy/kubernetes/overlays/current-host"
  and .status.sync.revision == $revision
  and .status.sync.status == "Synced"
  and .status.health.status == "Healthy"
' <<<"$application" >/dev/null

work_dir="$(mktemp -d)"
trap 'rm -rf -- "$work_dir"' EXIT
steady_render="$work_dir/steady.yaml"
operation_render="$work_dir/operation.yaml"
render "$steady_root" > "$steady_render"
render "$operation_root" > "$operation_render"

extract_object() {
  local manifest="$1"
  local kind="$2"
  local name="$3"
  local match_mode="$4"
  local output="$5"
  python3 - "$manifest" "$kind" "$name" "$match_mode" "$output" <<'PY'
import pathlib
import re
import sys

manifest, wanted_kind, wanted_name, match_mode, output = sys.argv[1:]
documents = re.split(r"\n---\s*\n", pathlib.Path(manifest).read_text())
matches = []
for document in documents:
    kind = re.search(r"(?m)^kind: ([^\s]+)$", document)
    names = re.findall(r"(?m)^  name: ([^\s]+)$", document)
    if not kind or len(names) != 1 or kind.group(1) != wanted_kind:
        continue
    matched = names[0] == wanted_name if match_mode == "exact" else names[0].startswith(wanted_name + "-")
    if matched:
        matches.append(document.rstrip() + "\n")
if len(matches) != 1:
    raise SystemExit(f"wanted one {wanted_kind} {match_mode} {wanted_name}, found {len(matches)}")
pathlib.Path(output).write_text(matches[0])
PY
}

for logical_name in keycloak-desired-state tenant-catalog; do
  steady_config_map="$work_dir/steady-$logical_name.yaml"
  operation_config_map="$work_dir/operation-$logical_name.yaml"
  extract_object "$steady_render" ConfigMap "$logical_name" prefix "$steady_config_map"
  extract_object "$operation_render" ConfigMap "$logical_name" prefix "$operation_config_map"
  cmp -s "$steady_config_map" "$operation_config_map"

  config_map_name="$(sed -n 's/^  name: //p' "$operation_config_map")"
  live_config_map="$("${kubectl_cmd[@]}" -n noebs get configmap "$config_map_name" -o json)"
  jq -e --arg name "$config_map_name" '
    .immutable == true
    and .metadata.labels["app.kubernetes.io/part-of"] == "noebs"
    and .metadata.annotations["argocd.argoproj.io/tracking-id"] == ("noebs:/ConfigMap:noebs/" + $name)
  ' <<<"$live_config_map" >/dev/null

  case "$logical_name" in
    keycloak-desired-state)
      data_key=keycloak-desired-state.yaml
      source_file="$authority_root/keycloak-desired-state.yaml"
      ;;
    tenant-catalog)
      data_key=tenant-catalog.yaml
      source_file="$authority_root/tenant-catalog.yaml"
      ;;
  esac
  jq -j --arg key "$data_key" '.data[$key] // empty' \
    <<<"$live_config_map" > "$work_dir/live-$data_key"
  cmp -s "$source_file" "$work_dir/live-$data_key"
done

job_manifest="$work_dir/job.yaml"
extract_object "$operation_render" Job noebs-keycloak-membership-assignment exact "$job_manifest"
job="$("${kubectl_cmd[@]}" apply --dry-run=server -f "$job_manifest" -o json)"
desired_state_name="$(sed -n 's/^  name: //p' "$work_dir/operation-keycloak-desired-state.yaml")"
tenant_catalog_name="$(sed -n 's/^  name: //p' "$work_dir/operation-tenant-catalog.yaml")"
jq -e --arg desired "$desired_state_name" --arg catalog "$tenant_catalog_name" '
  ([.spec.template.spec.volumes[] | select(.name == "desired-state") | .configMap.name] == [$desired])
  and ([.spec.template.spec.volumes[] | select(.name == "tenant-catalog") | .configMap.name] == [$catalog])
' <<<"$job" >/dev/null

"${kubectl_cmd[@]}" apply -f "$job_manifest"
