#!/bin/bash
# Deploy noebs to exe.dev using Docker Compose
# Prerequisites:
#   - SSH access to exe.dev configured
#   - VM created: ssh exe.dev new noebs
#   - Docker + Compose available on the VM

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VM_NAME="${1:-noebs}"

echo "=== Deploying noebs via Docker Compose to: $VM_NAME ==="

if [[ ! -f "$PROJECT_DIR/config.yaml" ]]; then
    echo "Error: config.yaml not found at $PROJECT_DIR/config.yaml" >&2
    exit 1
fi

if [[ ! -f "$PROJECT_DIR/secrets.yaml" ]]; then
    echo "Error: secrets.yaml not found at $PROJECT_DIR/secrets.yaml" >&2
    exit 1
fi

if [[ ! -f "$PROJECT_DIR/.sops/age-key.txt" ]]; then
    echo "Error: Age key not found at $PROJECT_DIR/.sops/age-key.txt" >&2
    exit 1
fi

echo "[1/3] Copying project files to exe.dev..."
rsync -avz --exclude='.git' --exclude='node_modules' --exclude='*.db' --exclude='__debug_bin' --exclude='.sops' \
    "$PROJECT_DIR/" "$VM_NAME.exe.xyz:/app/noebs/"

echo "[2/3] Copying age key to VM..."
ssh "$VM_NAME.exe.xyz" "mkdir -p /app/noebs/.sops"
scp "$PROJECT_DIR/.sops/age-key.txt" "$VM_NAME.exe.xyz:/app/noebs/.sops/age-key.txt"
ssh "$VM_NAME.exe.xyz" "chmod 600 /app/noebs/.sops/age-key.txt"

echo "[3/3] Building and starting containers..."
ssh "$VM_NAME.exe.xyz" << 'REMOTE_SCRIPT'
set -e
cd /app/noebs

if command -v docker >/dev/null 2>&1; then
    if docker compose version >/dev/null 2>&1; then
        docker compose up -d --build
    elif command -v docker-compose >/dev/null 2>&1; then
        docker-compose up -d --build
    else
        echo "Error: docker compose not found" >&2
        exit 1
    fi
else
    echo "Error: docker not found" >&2
    exit 1
fi
REMOTE_SCRIPT

echo "=== Deployment complete ==="
echo "VM: https://$VM_NAME.exe.xyz:8080"
echo "Logs: ssh $VM_NAME.exe.xyz 'cd /app/noebs && docker compose logs -f web'"
