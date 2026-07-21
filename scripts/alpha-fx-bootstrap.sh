#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 ]] || fail "usage: $0 <40-character-promotion-sha>"
promotion_sha=$1
[[ $promotion_sha =~ ^[0-9a-f]{40}$ ]] || fail "promotion SHA must be lowercase full-length hexadecimal"

for command in jq sudo k3s; do
  command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
done

readonly namespace=noebs
readonly temporal_image='docker.io/temporalio/auto-setup@sha256:f14912b699cf73015ad5c4fc18d522d4b014db90e794039214dfb7c022c2644f'
readonly job_name="wallet-fx-bootstrap-${promotion_sha}"
readonly workflow_id="wallet-fx-reference-bootstrap-${promotion_sha}"
kubectl=(sudo -n k3s kubectl)

wallet_sql() {
  local sql=$1
  "${kubectl[@]}" -n "$namespace" exec postgres-0 -- \
    sh -ceu 'exec gosu postgres psql -U postgres -d wallet_ledger -X -Aqt -v ON_ERROR_STOP=1 -c "$1"' sh "$sql"
}

fx_gate() {
  wallet_sql "
WITH enabled AS (
  SELECT pair.id, pair.base_currency_code, pair.quote_currency_code
  FROM public.fx_source_pairs pair
  JOIN public.fx_sources source ON source.id = pair.source_id
  WHERE source.code = 'ecb-reference'
    AND source.is_enabled
    AND pair.is_enabled
), availability AS (
  SELECT enabled.id,
         enabled.base_currency_code,
         enabled.quote_currency_code,
         EXISTS (
           SELECT 1
           FROM public.fx_observations observation
           WHERE observation.source_pair_id = enabled.id
             AND observation.observation_at <= clock_timestamp()
             AND observation.retrieved_at <= clock_timestamp()
             AND observation.created_at <= clock_timestamp()
             AND clock_timestamp() < observation.expires_at
         ) AS is_fresh
  FROM enabled
)
SELECT count(*)::text || '|' ||
       count(*) FILTER (WHERE NOT is_fresh)::text || '|' ||
       COALESCE(string_agg(
         base_currency_code || '/' || quote_currency_code,
         ',' ORDER BY base_currency_code, quote_currency_code
       ), '')
FROM availability"
}

require_fx_gate() {
  local result
  result=$(fx_gate)
  if [[ $result != '4|0|EUR/CHF,EUR/GBP,EUR/JPY,EUR/USD' ]]; then
    fail "FX postcondition failed (enabled|missing-fresh|pairs=$result)"
  fi
}

application_json=$("${kubectl[@]}" -n argocd get application noebs -o json)
if ! jq -e --arg revision "$promotion_sha" '
  .spec.source.targetRevision == $revision and
  .status.sync.revision == $revision and
  .status.sync.status == "Synced" and
  .status.health.status == "Healthy"
' >/dev/null <<<"$application_json"; then
  jq '{target: .spec.source.targetRevision, revision: .status.sync.revision, sync: .status.sync.status, health: .status.health.status}' \
    <<<"$application_json" >&2
  fail "Argo CD noebs Application is not Synced and Healthy at the exact promotion SHA"
fi

worker_json=$("${kubectl[@]}" -n "$namespace" get deployment wallet-worker -o json)
if ! jq -e '
  .status.observedGeneration == .metadata.generation and
  .spec.replicas >= 1 and
  (.status.updatedReplicas // 0) == .spec.replicas and
  (.status.readyReplicas // 0) == .spec.replicas and
  (.status.availableReplicas // 0) == .spec.replicas
' >/dev/null <<<"$worker_json"; then
  jq '{generation: .metadata.generation, observed: .status.observedGeneration, desired: .spec.replicas, updated: .status.updatedReplicas, ready: .status.readyReplicas, available: .status.availableReplicas}' \
    <<<"$worker_json" >&2
  fail "wallet-worker is not fully updated and Ready with at least one replica"
fi

migration_versions=$(wallet_sql "
SELECT COALESCE(
  string_agg(version_id::text || ':' || is_applied::text, ',' ORDER BY version_id),
  ''
)
FROM public.goose_db_version_wallet_ledger")
[[ $migration_versions == '0:true,1:true,2:true' ]] || \
  fail "wallet migration set is not exactly 0:true,1:true,2:true (got $migration_versions)"

"${kubectl[@]}" -n "$namespace" rollout status deployment/wallet-worker --timeout=5m >/dev/null

if [[ $(fx_gate) == '4|0|EUR/CHF,EUR/GBP,EUR/JPY,EUR/USD' ]]; then
  printf 'FX observations are already fresh for the exact enabled ECB catalog\n'
  printf 'workflow_id=%s\n' "$workflow_id"
  exit 0
fi

read -r -d '' bootstrap_script <<'SCRIPT' || true
client_secret=$(</etc/noebs-temporal/client-secret)
case "$client_secret" in
  ''|*[!A-Za-z0-9._~-]*) printf 'invalid client secret\n' >&2; exit 1 ;;
esac
response=$(
  printf '%s\n' \
    'fail' \
    'silent' \
    'show-error' \
    'cacert = "/etc/noebs-keycloak/ca.pem"' \
    "user = \"noebs-temporal-namespace-bootstrap:$client_secret\"" \
    'data-urlencode = "grant_type=client_credentials"' \
    'url = "https://keycloak.noebs.svc.cluster.local:8443/auth/realms/noebs/protocol/openid-connect/token"' |
    curl --disable --config -
)
unset client_secret
token=$(printf '%s' "$response" | sed -n 's/.*"access_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
case "$token" in
  ''|*[!A-Za-z0-9._~-]*) printf 'invalid access token response\n' >&2; exit 1 ;;
esac
export TEMPORAL_API_KEY=$token
unset token response
temporal --disable-config-file workflow execute \
  --address temporal-frontend:7233 \
  --namespace default \
  --tls \
  --tls-ca-path /etc/noebs-temporal/ca.pem \
  --tls-server-name temporal-frontend \
  --command-timeout 15m \
  --workflow-id "$BOOTSTRAP_WORKFLOW_ID" \
  --id-reuse-policy AllowDuplicateFailedOnly \
  --id-conflict-policy UseExisting \
  --type FXReferenceSync \
  --task-queue wallet-main \
  --output json
SCRIPT

job_manifest=$(jq -n \
  --arg job "$job_name" \
  --arg workflow_id "$workflow_id" \
  --arg image "$temporal_image" \
  --arg script "$bootstrap_script" '
  {
    apiVersion: "batch/v1",
    kind: "Job",
    metadata: {
      name: $job,
      namespace: "noebs",
      labels: {"app.kubernetes.io/name": "temporal-namespace-bootstrap"}
    },
    spec: {
      backoffLimit: 0,
      activeDeadlineSeconds: 1200,
      ttlSecondsAfterFinished: 600,
      template: {
        metadata: {labels: {"app.kubernetes.io/name": "temporal-namespace-bootstrap"}},
        spec: {
          serviceAccountName: "temporal-namespace-bootstrap",
          automountServiceAccountToken: false,
          restartPolicy: "Never",
          securityContext: {
            runAsNonRoot: true,
            runAsUser: 10001,
            runAsGroup: 10001,
            fsGroup: 10001
          },
          containers: [{
            name: "bootstrap",
            image: $image,
            imagePullPolicy: "IfNotPresent",
            command: ["/bin/bash", "-ceu", "--"],
            args: [$script],
            env: [{name: "BOOTSTRAP_WORKFLOW_ID", value: $workflow_id}],
            resources: {
              requests: {cpu: "25m", memory: "64Mi"},
              limits: {cpu: "500m", memory: "256Mi"}
            },
            securityContext: {
              allowPrivilegeEscalation: false,
              capabilities: {drop: ["ALL"]},
              readOnlyRootFilesystem: true
            },
            volumeMounts: [
              {
                name: "temporal-authority",
                mountPath: "/etc/noebs-temporal/ca.pem",
                subPath: "ca.pem",
                readOnly: true
              },
              {
                name: "temporal-authority",
                mountPath: "/etc/noebs-temporal/client-secret",
                subPath: "client-secret",
                readOnly: true
              },
              {
                name: "keycloak-ca",
                mountPath: "/etc/noebs-keycloak/ca.pem",
                subPath: "ca.pem",
                readOnly: true
              }
            ]
          }],
          volumes: [
            {
              name: "temporal-authority",
              secret: {secretName: "temporal-namespace-bootstrap-credentials"}
            },
            {
              name: "keycloak-ca",
              secret: {secretName: "keycloak-transport-ca"}
            }
          ]
        }
      }
    }
  }
')

if existing_job=$("${kubectl[@]}" -n "$namespace" get "job/$job_name" -o json 2>/dev/null); then
  if jq -e 'any(.status.conditions[]?; .type == "Complete" and .status == "True")' >/dev/null <<<"$existing_job"; then
    "${kubectl[@]}" -n "$namespace" logs "job/$job_name"
    require_fx_gate
    printf 'workflow_id=%s\n' "$workflow_id"
    exit 0
  fi
  if jq -e 'any(.status.conditions[]?; .type == "Failed" and .status == "True")' >/dev/null <<<"$existing_job"; then
    "${kubectl[@]}" -n "$namespace" logs "job/$job_name" >&2 || true
    fail "existing FX bootstrap Job failed; inspect it before an explicit retry"
  fi
else
  printf '%s\n' "$job_manifest" | "${kubectl[@]}" apply -f -
fi
unset job_manifest

job_complete=false
for ((attempt = 0; attempt < 240; attempt++)); do
  job_json=$("${kubectl[@]}" -n "$namespace" get "job/$job_name" -o json)
  if jq -e 'any(.status.conditions[]?; .type == "Complete" and .status == "True")' >/dev/null <<<"$job_json"; then
    job_complete=true
    break
  fi
  if jq -e 'any(.status.conditions[]?; .type == "Failed" and .status == "True")' >/dev/null <<<"$job_json"; then
    "${kubectl[@]}" -n "$namespace" logs "job/$job_name" >&2 || true
    "${kubectl[@]}" -n "$namespace" describe "job/$job_name" >&2 || true
    fail "FX bootstrap Job failed"
  fi
  sleep 5
done

if [[ $job_complete != true ]]; then
  "${kubectl[@]}" -n "$namespace" logs "job/$job_name" >&2 || true
  "${kubectl[@]}" -n "$namespace" describe "job/$job_name" >&2 || true
  fail "FX bootstrap Job did not complete within 20 minutes"
fi

"${kubectl[@]}" -n "$namespace" logs "job/$job_name"
require_fx_gate
printf 'workflow_id=%s\n' "$workflow_id"
