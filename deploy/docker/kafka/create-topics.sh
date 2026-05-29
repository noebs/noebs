#!/usr/bin/env bash
set -euo pipefail

bootstrap_server_file="/mnt/shared/config/bootstrap-server"
topics_file="/mnt/shared/config/topics.txt"

read_required_file() {
  local label="$1"
  local path="$2"
  if [ ! -s "$path" ]; then
    echo "missing $label file: $path" >&2
    exit 1
  fi
  tr -d '\r\n' < "$path"
}

bootstrap_server="$(read_required_file "Kafka bootstrap server" "$bootstrap_server_file")"
if [ "$bootstrap_server" = "" ]; then
  echo "Kafka bootstrap server file is empty: $bootstrap_server_file" >&2
  exit 1
fi
if [ ! -s "$topics_file" ]; then
  echo "missing Kafka topics file: $topics_file" >&2
  exit 1
fi

deadline=$((SECONDS + 600))
until /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server "$bootstrap_server" >/dev/null 2>&1; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "Kafka bootstrap server is not ready: $bootstrap_server" >&2
    exit 1
  fi
  sleep 5
done

while read -r topic partitions replication_factor; do
  if [ "$topic" = "" ]; then
    continue
  fi
  if [ "$partitions" = "" ] || [ "$replication_factor" = "" ]; then
    echo "invalid Kafka topic declaration: $topic $partitions $replication_factor" >&2
    exit 1
  fi
  /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "$bootstrap_server" \
    --create \
    --if-not-exists \
    --topic "$topic" \
    --partitions "$partitions" \
    --replication-factor "$replication_factor"
done < "$topics_file"
