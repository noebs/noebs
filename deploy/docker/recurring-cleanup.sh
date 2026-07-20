#!/usr/bin/env bash
set -euo pipefail

readonly interval_seconds=300

while true; do
  /entrypoint.sh
  sleep "$interval_seconds"
done
