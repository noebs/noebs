#!/usr/bin/env bash
set -euo pipefail

config_file="/mnt/shared/config/server.properties"
cluster_id_file="/mnt/shared/config/cluster.id"
log_dir="/var/lib/kafka/data"
meta_file="$log_dir/meta.properties"

read_required_file() {
  local label="$1"
  local path="$2"
  if [ ! -s "$path" ]; then
    echo "missing $label file: $path" >&2
    exit 1
  fi
  tr -d '\r\n' < "$path"
}

if [ ! -s "$config_file" ]; then
  echo "missing Kafka server properties file: $config_file" >&2
  exit 1
fi

cluster_id="$(read_required_file "Kafka cluster id" "$cluster_id_file")"
if [ "$cluster_id" = "" ]; then
  echo "Kafka cluster id file is empty: $cluster_id_file" >&2
  exit 1
fi

mkdir -p "$log_dir"
if [ ! -w "$log_dir" ]; then
  echo "Kafka log directory is not writable: $log_dir" >&2
  exit 1
fi

if [ ! -s "$meta_file" ]; then
  /opt/kafka/bin/kafka-storage.sh format --standalone -t "$cluster_id" -c "$config_file"
fi

exec /opt/kafka/bin/kafka-server-start.sh "$config_file"
