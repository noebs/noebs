#!/bin/bash
# Deploy noebs to exe.dev using Docker Compose
# Usage:
#   ./scripts/deploy-exe.dev.sh <vm-name> [--public|--private] [--port 8080]
#   ./scripts/deploy-exe.dev.sh --name <vm-name> [--public|--private] [--port 8080]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VM_NAME=""
VISIBILITY="public"
PORT="8080"
EXE_SSH_OPTS=(-o StrictHostKeyChecking=accept-new)
VM_SSH_OPTS=(-o StrictHostKeyChecking=accept-new)

if [[ $# -gt 0 ]] && [[ "$1" != --* ]]; then
    VM_NAME="$1"
    shift || true
fi

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            VM_NAME="$2"
            shift 2
            ;;
        --public)
            VISIBILITY="public"
            shift
            ;;
        --private)
            VISIBILITY="private"
            shift
            ;;
        --port)
            PORT="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$VM_NAME" ]]; then
    echo "Usage: $0 <vm-name> [--public|--private] [--port 8080]" >&2
    echo "   or: $0 --name <vm-name> [--public|--private] [--port 8080]" >&2
    exit 1
fi

require_file() {
    if [[ ! -f "$1" ]]; then
        echo "Error: missing $1" >&2
        exit 1
    fi
}

require_file "$PROJECT_DIR/config.yaml"
require_file "$PROJECT_DIR/secrets.yaml"
require_file "$PROJECT_DIR/docker-compose.yml"
require_file "$PROJECT_DIR/.sops/age-key.txt"

echo "=== Deploying noebs to exe.dev VM: $VM_NAME ==="

echo "[1/6] Ensuring VM exists..."
if VM_LIST="$(ssh "${EXE_SSH_OPTS[@]}" exe.dev ls --json)"; then
    if ! echo "$VM_LIST" | grep -Eq "\"vm_name\"[[:space:]]*:[[:space:]]*\"$VM_NAME\""; then
        ssh "${EXE_SSH_OPTS[@]}" exe.dev new --name="$VM_NAME"
    fi
else
    echo "Error: failed to reach exe.dev. Make sure you're logged in (ssh exe.dev)" >&2
    exit 1
fi

echo "[2/6] Configuring exe.dev proxy..."
ssh "${EXE_SSH_OPTS[@]}" exe.dev share port "$VM_NAME" "$PORT"
if [[ "$VISIBILITY" == "public" ]]; then
    ssh "${EXE_SSH_OPTS[@]}" exe.dev share set-public "$VM_NAME"
else
    ssh "${EXE_SSH_OPTS[@]}" exe.dev share set-private "$VM_NAME"
fi

echo "[3/6] Copying project files to VM..."
echo "Waiting for VM SSH..."
for attempt in $(seq 1 30); do
    if ssh "${VM_SSH_OPTS[@]}" -o BatchMode=yes -o ConnectTimeout=5 "$VM_NAME.exe.xyz" "true" >/dev/null 2>&1; then
        break
    fi
    if [[ "$attempt" -eq 30 ]]; then
        echo "Error: VM did not become reachable over SSH" >&2
        exit 1
    fi
    sleep 2
done
ssh "${VM_SSH_OPTS[@]}" "$VM_NAME.exe.xyz" "sudo mkdir -p /app/noebs && sudo chown \$(id -u):\$(id -g) /app/noebs"
rsync -avz --delete --exclude='.git' --exclude='node_modules' --exclude='*.db' --exclude='__debug_bin' --exclude='.sops' \
    -e "ssh -o StrictHostKeyChecking=accept-new" \
    "$PROJECT_DIR/" "$VM_NAME.exe.xyz:/app/noebs/"

echo "[4/6] Copying age key to VM..."
ssh "${VM_SSH_OPTS[@]}" "$VM_NAME.exe.xyz" "mkdir -p /app/noebs/.sops"
scp "${VM_SSH_OPTS[@]}" "$PROJECT_DIR/.sops/age-key.txt" "$VM_NAME.exe.xyz:/app/noebs/.sops/age-key.txt"
ssh "${VM_SSH_OPTS[@]}" "$VM_NAME.exe.xyz" "chmod 600 /app/noebs/.sops/age-key.txt"

echo "[5/6] Ensuring Docker + Compose are available..."
ssh "${VM_SSH_OPTS[@]}" "$VM_NAME.exe.xyz" << 'REMOTE_SCRIPT'
set -euo pipefail

if ! command -v curl >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
        sudo apt-get update -qq
        sudo apt-get install -y -qq curl
    fi
fi

if ! command -v docker >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sudo sh
fi

if command -v systemctl >/dev/null 2>&1; then
    sudo systemctl enable --now docker || true
fi

if ! sudo docker compose version >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
        sudo apt-get update -qq
        sudo apt-get install -y -qq docker-compose-plugin
    fi
fi

sudo docker compose version >/dev/null 2>&1
REMOTE_SCRIPT

echo "[6/6] Building and starting containers..."
ssh "${VM_SSH_OPTS[@]}" "$VM_NAME.exe.xyz" << 'REMOTE_SCRIPT'
set -euo pipefail
cd /app/noebs
sudo docker compose up -d --build
REMOTE_SCRIPT

echo "=== Deployment complete ==="
echo "VM: https://$VM_NAME.exe.xyz/"
if [[ "$VISIBILITY" == "private" ]]; then
    echo "Access is private. Use: ssh exe.dev share add $VM_NAME you@example.com"
fi
