#!/usr/bin/env bash
set -euo pipefail
source /etc/noebs/backup.env
umask 077
age_recipients=(-r age1jmq9nl0haxetduys37jfj7ha93vee9pd7me7mtyw5ztm4n8flyrse3mwum -r age1haq4w7h3xh2cm73yesfrv82drmhhdwqmykr3hccltaac582ggdhqlruhra)
exec 9>/var/lib/noebs-backup.lock
flock -n 9
work=$(mktemp -d /var/lib/noebs-backup.XXXXXXXX)
trap 'rm -rf "$work"' EXIT
stamp=$(date -u +%Y%m%dT%H%M%SZ)
kubectl=(/usr/local/bin/k3s kubectl -n noebs)

# Older workflow history can safely replay against newer committed ledger receipts.
"${kubectl[@]}" exec temporal-postgres-0 -- bash -ec 'export PGPASSWORD=$(cat /opt/temporal-postgres/secrets/password); exec pg_dumpall -h /var/run/postgresql -U temporal' \
  | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-temporal.sql.gz.age"
"${kubectl[@]}" exec noebs-mojaloop-redis-0 -- sh -ec 'umask 077; snapshot=$(mktemp); cleanup() { rm -f "$snapshot"; }; trap cleanup EXIT; redis-cli --rdb "$snapshot" >/dev/null; cat "$snapshot"' \
  | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-mojaloop.rdb.age"
"${kubectl[@]}" exec postgres-0 -- gosu postgres pg_dumpall -h /var/run/postgresql \
  | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-postgres.sql.gz.age"
"${kubectl[@]}" exec keycloak-postgres-0 -- bash -ec 'export PGPASSWORD=$(cat /opt/keycloak-postgres/secrets/password); exec pg_dumpall -h /var/run/postgresql -U keycloak' \
  | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-keycloak.sql.gz.age"
"${kubectl[@]}" get all,pvc,configmaps,secrets -o json \
  | gzip | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-kubernetes.json.gz.age"
sqlite3 /var/lib/rancher/k3s/server/db/state.db ".backup '$work/state.db'"
tar czf - -C "$work" state.db -C /var/lib/rancher/k3s/server token cred \
  | /usr/local/bin/age "${age_recipients[@]}" -o "$work/$stamp-control-plane.tar.gz.age"
(
  cd "$work"
  sha256sum ./*.age > "$stamp-SHA256SUMS"
)
for file in "$work"/*.age "$work"/*-SHA256SUMS; do
  printf 'put %s %s\n' "$file" "$(basename "$file")"
done | sftp -q -P 2222 -b - -i /etc/noebs/backup-key -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/etc/noebs/backup-known-hosts "noebsbackup@$BACKUP_IP"
printf 'Completed encrypted noebs backup %s\n' "$stamp"
