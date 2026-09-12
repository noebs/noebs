#!/usr/bin/env python3
"""Provision exe.dev VMs; serialize authoritative state on the control VM."""
import argparse
import io
import json
import os
from pathlib import Path
import shlex
import subprocess
import tarfile
import uuid

ROOT = Path(__file__).resolve().parents[2]


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def ssh_args(key, destination):
    return ["ssh", "-i", str(key), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
            "-o", "UserKnownHostsFile="+str(ROOT/'deploy/exe/known_hosts'), "-o", "ConnectTimeout=20", "-o", "ConnectionAttempts=3",
            "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", destination]


def ssh(key, destination, command, **kwargs):
    return run(ssh_args(key, destination) + [command], **kwargs)


class RemoteLease:
    def __init__(self, command):
        self.command = command
        self.process = None

    def __enter__(self):
        self.process = subprocess.Popen(self.command + ["flock -n /var/lib/noebs/release.lock sh -c 'printf \"locked\\n\"; cat >/dev/null'"],
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if self.process.stdout.readline() != b'locked\n':
            self.process.stdin.close()
            self.process.wait(timeout=30)
            raise RuntimeError('Another release or migration holds the controller lease, or the controller is unavailable')
        return self

    def check(self):
        if self.process.poll() is not None:
            raise RuntimeError('Controller release lease was lost; further mutations are fenced')

    def __exit__(self, kind, value, traceback):
        self.process.stdin.close()
        self.process.wait(timeout=30)
        if kind is None and self.process.returncode:
            raise RuntimeError('Controller release lease ended unexpectedly')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--key", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    args = parser.parse_args()
    key, work = args.key.resolve(), args.work.resolve()
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    fleet = json.loads((ROOT / "foundation/exedev/fleet.json").read_text())
    vms = json.loads(ssh(key, "exe.dev", "ls --json", capture_output=True).stdout)["vms"]
    controllers = [vm for vm in vms if vm["vm_name"] == "noebs-control"]
    local_state = work / "bootstrap"
    run([str(ROOT / "scripts/exe/install-terraform.sh"), str(work / "bin")])
    env = os.environ | {"PATH": str(work / "bin") + ":" + os.environ["PATH"], "EXE_SSH_KEY": str(key), "EXE_TERRAFORM_DIR": str(local_state)}
    if not controllers:
        control_vars = work / "control.json"
        control_vars.write_text(json.dumps({"machines": {"noebs-control": fleet["machines"]["noebs-control"]}}))
        run([str(ROOT / "scripts/exe/terraform.sh"), "apply", "-input=false", "-auto-approve", "-no-color", "-var-file=" + str(control_vars)], env=env)
        controller = json.loads(run([str(work / "bin/terraform"), "-chdir=" + str(local_state), "output", "-json", "machines"], capture_output=True).stdout)["noebs-control"]["ssh_destination"]
    else:
        controller = controllers[0]["ssh_dest"]

    remote_state = "/var/lib/noebs/foundation"
    state_exists = ssh(key, controller, f"test -s {remote_state}/terraform.tfstate && echo present || echo absent", capture_output=True).stdout.strip() == b"present"
    if not state_exists:
        state_file = local_state / "terraform.tfstate"
        if not state_file.is_file():
            raise RuntimeError("Controller exists without authoritative state. Restore its encrypted state backup; do not import or recreate automatically.")
        ssh(key, controller, f"install -d -m 0700 {remote_state}; cat > {remote_state}/terraform.tfstate; chmod 0600 {remote_state}/terraform.tfstate", input=state_file.read_bytes())
        # State has one writable home. Recovery copies are encrypted below.
        for old in local_state.glob("*.tfstate*"):
            old.unlink()

    ssh(key, controller, "command -v flock >/dev/null || (export DEBIAN_FRONTEND=noninteractive; apt-get update -qq; apt-get install -y -qq util-linux)")

    provider = work / "terraform-provider-exedev_v0.1.0"
    run(["go", "build", "-trimpath", "-o", str(provider), "."], cwd=ROOT / "foundation/exedev-provider", env=os.environ | {"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64"})
    files = {"terraform": work / "bin/terraform", "terraform-provider-exedev_v0.1.0": provider, "main.tf": ROOT / "foundation/exedev/main.tf", "fleet.json": ROOT / "foundation/exedev/fleet.json"}
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w:gz") as bundle:
        for name, file in files.items():
            bundle.add(file, arcname=name)
    incoming = "/var/lib/noebs/incoming/" + uuid.uuid4().hex
    ssh(key, controller, f"install -d -m 0700 {incoming}; tar xz -C {incoming}", input=archive.getvalue())
    token = run(["python3", str(ROOT / "scripts/exe/api-token.py"), str(key)], capture_output=True).stdout
    command = "incoming=" + shlex.quote(incoming) + "\n" + r'''set -eu
trap 'rm -rf "$incoming"' EXIT
IFS= read -r EXEDEV_TOKEN
export EXEDEV_TOKEN
exec 8>/var/lib/noebs/release.lock
flock -n 8
exec 9>/var/lib/noebs/foundation.lock
flock -x 9
cd /var/lib/noebs/foundation
install -d providers/registry.terraform.io/noebs/exedev/0.1.0/linux_amd64
install -m 0755 "$incoming/terraform-provider-exedev_v0.1.0" providers/registry.terraform.io/noebs/exedev/0.1.0/linux_amd64/
install -m 0755 "$incoming/terraform" /usr/local/bin/terraform
cp "$incoming/main.tf" "$incoming/fleet.json" .
cat > terraform.rc <<'CONFIG'
provider_installation { filesystem_mirror { path = "/var/lib/noebs/foundation/providers" } }
CONFIG
export TF_CLI_CONFIG_FILE=/var/lib/noebs/foundation/terraform.rc
rm -f .terraform.lock.hcl
terraform init -input=false -no-color >/dev/null
terraform plan -input=false -no-color -var-file=fleet.json -out=release.tfplan
terraform apply -input=false -no-color release.tfplan
terraform output -json machines > machines.json
'''
    try:
        ssh(key, controller, "bash -c " + shlex.quote(command), input=token)
    finally:
        # Preserve state even when an API mutation partly succeeds and apply returns an error.
        state = ssh(key, controller, f"cat {remote_state}/terraform.tfstate", capture_output=True).stdout
        encrypted = run(["sops", "--encrypt", "--input-type", "json", "--output-type", "json", "--age", "age1jmq9nl0haxetduys37jfj7ha93vee9pd7me7mtyw5ztm4n8flyrse3mwum,age1haq4w7h3xh2cm73yesfrv82drmhhdwqmykr3hccltaac582ggdhqlruhra", "--filename-override", "terraform-state.enc.yaml", "/dev/stdin"], input=state, capture_output=True).stdout
        (work / "terraform-state.enc.json").write_bytes(encrypted)
    machines = json.loads(ssh(key, controller, f"cat {remote_state}/machines.json", capture_output=True).stdout)
    (work / "machines.json").write_text(json.dumps(machines, indent=2) + "\n")
    backup = machines["noebs-backup"]["ssh_destination"]
    ssh(key, backup, "sudo install -d -m 0700 /var/lib/noebs-backup; sudo tee /var/lib/noebs-backup/terraform-state.enc.json >/dev/null", input=encrypted)


if __name__ == "__main__":
    main()
