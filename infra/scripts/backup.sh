#!/usr/bin/env bash
set -euo pipefail
source /etc/noebs/backup.env
umask 077
mode=${1:?snapshot or publish required}
checkpoint=${2:?coordinated checkpoint path required}
python3 /usr/local/lib/noebs-backup-checkpoint.py "$mode" "$checkpoint"
readarray -t fields < <(python3 - "$checkpoint" <<'PY'
import json,sys
value=json.load(open(sys.argv[1]))
print(value['id'])
print(value['cold_volumes']['kafka'])
PY
)
stamp=${fields[0]}
test "$checkpoint" = "/var/lib/noebs/backups/$stamp/checkpoint.json"
work=$(dirname "$checkpoint")/archives
age_recipients=(-r age1jmq9nl0haxetduys37jfj7ha93vee9pd7me7mtyw5ztm4n8flyrse3mwum -r age1haq4w7h3xh2cm73yesfrv82drmhhdwqmykr3hccltaac582ggdhqlruhra)
exec 9>/var/lib/noebs-backup.lock
flock -n 9
kubectl=(/usr/local/bin/k3s kubectl -n noebs)

if [[ "$mode" == snapshot ]]; then
  test "$("${kubectl[@]}" get configmap noebs-backup-checkpoint -o jsonpath='{.data.checkpoint_id}')" = "$stamp"
  mkdir -m 0700 "$work"
  for name in kafka; do
    test "$("${kubectl[@]}" get statefulset "$name" -o jsonpath='{.spec.replicas}')" = 0
    test -z "$("${kubectl[@]}" get pod "$name-0" --ignore-not-found -o name)"
  done
  tar czf - -C "${fields[1]}" . | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-kafka.tar.gz.age"
  "${kubectl[@]}" exec temporal-postgres-0 -- bash -ec 'export PGPASSWORD=$(cat /opt/temporal-postgres/secrets/password); exec pg_dumpall -h /var/run/postgresql -U temporal' \
    | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-temporal.sql.gz.age"
  "${kubectl[@]}" exec postgres-0 -- gosu postgres pg_dumpall -h /var/run/postgresql \
    | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-postgres.sql.gz.age"
  "${kubectl[@]}" exec keycloak-postgres-0 -- bash -ec 'export PGPASSWORD=$(cat /opt/keycloak-postgres/secrets/password); exec pg_dumpall -h /var/run/postgresql -U keycloak' \
    | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-keycloak.sql.gz.age"
  "${kubectl[@]}" get all,pvc,configmaps,secrets -o json \
    | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-kubernetes.json.gz.age"
  state=$(mktemp)
  trap 'rm -f "$state"' EXIT
  sqlite3 /var/lib/rancher/k3s/server/db/state.db ".backup '$state'"
  tar czf - --transform="s|^$(basename "$state")$|state.db|" -C "$(dirname "$state")" "$(basename "$state")" -C /var/lib/rancher/k3s/server token cred \
    | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-control-plane.tar.gz.age"
  printf 'Captured fenced noebs snapshot %s\n' "$stamp"
elif [[ "$mode" == publish ]]; then
  cp "$checkpoint" "$work/$stamp-checkpoint.json"
  (cd "$work"; sha256sum ./*.age "./$stamp-checkpoint.json" > "$stamp-SHA256SUMS")
  for file in "$work"/*.age "$work"/*-checkpoint.json "$work"/*-SHA256SUMS; do
    printf 'put %s %s\n' "$file" "$(basename "$file")"
  done | sftp -q -P 2222 -b - -i /etc/noebs/backup-key -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/etc/noebs/backup-known-hosts "noebsbackup@$BACKUP_IP"
  rm -rf -- "$work"
  printf 'Published coordinated noebs backup %s\n' "$stamp"
fi
