#!/usr/bin/env python3
import argparse
import json
import io
import tarfile
import os
from pathlib import Path
import shlex

from reconcile import ROOT, RemoteLease, run, ssh, ssh_args


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--enable-schedule', action='store_true')
    args=parser.parse_args()
    machines=json.loads(args.machines.read_text())
    with RemoteLease(ssh_args(args.key.resolve(),machines['noebs-control']['ssh_destination'])) as lease:
        setup(args,machines,lease)


def setup(args,machines,lease):
    key=args.key.resolve()
    data=machines['noebs-data']['ssh_destination']; backup=machines['noebs-backup']['ssh_destination']
    if ssh(key,data,'sudo k3s kubectl -n noebs get configmap noebs-backup-checkpoint --ignore-not-found -o name',capture_output=True).stdout:
        raise RuntimeError('Backup setup refused: an earlier checkpoint has not resumed')
    marker=ssh(key,data,'sudo k3s kubectl -n noebs get configmap noebs-migration --ignore-not-found -o json',capture_output=True).stdout
    if marker and json.loads(marker)['data']['state']!='destination-active':
        raise RuntimeError('Backup setup refused while migration or recovery is fenced')
    lease.check()
    binary_dir=args.work.resolve()/'bin'; binary_dir.mkdir(parents=True,exist_ok=True)
    run(['go','install','filippo.io/age/cmd/age@v1.2.1'],env=os.environ|{'GOBIN':str(binary_dir),'CGO_ENABLED':'0','GOOS':'linux','GOARCH':'amd64'})
    ssh(key,data,'command -v sqlite3 >/dev/null || (sudo apt-get update -qq && sudo apt-get install -y -qq sqlite3)')
    ssh(key,data,'sudo tee /usr/local/bin/age >/dev/null; sudo chmod 0755 /usr/local/bin/age',input=(binary_dir/'age').read_bytes())
    ssh(key,data,"sudo install -d -m 0700 /etc/noebs; sudo test -f /etc/noebs/backup-key || sudo ssh-keygen -q -t ed25519 -N '' -f /etc/noebs/backup-key")
    public=ssh(key,data,'sudo cat /etc/noebs/backup-key.pub',capture_output=True).stdout
    address=ssh(key,backup,'tailscale ip -4',capture_output=True).stdout.decode().strip()
    receiver=r'''set -eu
getent passwd noebsbackup >/dev/null || useradd --system --create-home --home-dir /var/lib/noebs-backup --shell /bin/sh noebsbackup
install -d -o root -g root -m 0755 /var/lib/noebs-backup
install -d -o noebsbackup -g noebsbackup -m 0700 /var/lib/noebs-backup/data
install -d -o root -g root -m 0755 /var/lib/noebs-backup/.ssh
IFS= read -r public_key
printf 'restrict %s\n' "$public_key" > /var/lib/noebs-backup/.ssh/authorized_keys
chmod 0644 /var/lib/noebs-backup/.ssh/authorized_keys
ssh-keygen -A
cat > /etc/noebs-backup-sshd.conf <<CONFIG
Port 2222
ListenAddress $backup_address
HostKey /etc/ssh/ssh_host_ed25519_key
AuthorizedKeysFile /var/lib/noebs-backup/.ssh/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
AllowUsers noebsbackup
UsePAM yes
AllowTcpForwarding no
X11Forwarding no
PermitTunnel no
ChrootDirectory /var/lib/noebs-backup
ForceCommand internal-sftp -d /data
Subsystem sftp internal-sftp
CONFIG
cat > /etc/systemd/system/noebs-backup-sshd.service <<'UNIT'
[Unit]
Description=Private noebs backup SFTP receiver
After=tailscaled.service
Requires=tailscaled.service
[Service]
ExecStart=/usr/sbin/sshd -D -e -f /etc/noebs-backup-sshd.conf
RuntimeDirectory=sshd
Restart=on-failure
[Install]
WantedBy=multi-user.target
UNIT
install -d -m 0755 /run/sshd
/usr/sbin/sshd -t -f /etc/noebs-backup-sshd.conf
systemctl daemon-reload
systemctl enable noebs-backup-sshd
systemctl restart noebs-backup-sshd
cat > /etc/systemd/system/noebs-backup-retention.service <<'UNIT'
[Unit]
Description=Retain fourteen days of nightly encrypted noebs backups
[Service]
Type=oneshot
ExecStart=/usr/bin/find /var/lib/noebs-backup/data -maxdepth 1 -type f -mtime +14 -delete
UNIT
cat > /etc/systemd/system/noebs-backup-retention.timer <<'UNIT'
[Unit]
Description=Remove expired encrypted noebs backup files
[Timer]
OnCalendar=daily
Persistent=true
[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now noebs-backup-retention.timer
'''
    ssh(key,backup,'sudo bash -c '+shlex.quote('backup_address='+shlex.quote(address)+'\n'+receiver),input=public)
    address=ssh(key,backup,'tailscale ip -4',capture_output=True).stdout.decode().strip()
    hostkey=ssh(key,backup,'sudo cat /etc/ssh/ssh_host_ed25519_key.pub',capture_output=True).stdout.decode().strip()
    ssh(key,data,'sudo tee /etc/noebs/backup-known-hosts >/dev/null',input=('[%s]:2222 '%address+hostkey+'\n').encode())
    config='BACKUP_IP='+address+'\n'
    ssh(key,data,'sudo tee /etc/noebs/backup.env >/dev/null',input=config.encode())
    ssh(key,data,'sudo tee /usr/local/sbin/noebs-backup >/dev/null; sudo chmod 0755 /usr/local/sbin/noebs-backup',input=(ROOT/'scripts/exe/backup.sh').read_bytes())
    ssh(key,data,'sudo tee /usr/local/lib/noebs-backup-checkpoint.py >/dev/null',input=(ROOT/'scripts/exe/backup_checkpoint.py').read_bytes())
    unit='[Unit]\nDescription=Encrypted noebs PostgreSQL and control-plane backup\nAfter=k3s.service tailscaled.service\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/noebs-backup\n'
    timer='[Unit]\nDescription=Hourly noebs backup\n[Timer]\nOnCalendar=hourly\nPersistent=true\nRandomizedDelaySec=60\n[Install]\nWantedBy=timers.target\n'
    ssh(key,data,'sudo tee /etc/systemd/system/noebs-backup.service >/dev/null',input=unit.encode())
    ssh(key,data,'sudo tee /etc/systemd/system/noebs-backup.timer >/dev/null',input=timer.encode())
    lease.check()
    ssh(key,data,'sudo systemctl daemon-reload; sudo systemctl disable --now noebs-backup.timer')

    files = {('scripts/exe/' + name): (ROOT / 'scripts/exe' / name).read_bytes()
             for name in ['coordinated_backup.py', 'reconcile.py', 'backup_checkpoint.py']}
    files['deploy/exe/known_hosts'] = (ROOT / 'deploy/exe/known_hosts').read_bytes()
    files['runtime/machines.json'] = json.dumps(machines).encode()
    files['runtime/ssh-key'] = key.read_bytes()
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w:gz') as bundle:
        for name, payload in files.items():
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode = len(payload), 0o600
            bundle.addfile(entry, io.BytesIO(payload))
    lease.check()
    ssh(key,backup,'sudo install -d -m 0700 /opt/noebs-backup && sudo tar xz -C /opt/noebs-backup',input=archive.getvalue())
    schedule = '''set -eu
cat > /etc/systemd/system/noebs-coordinated-backup.service <<'UNIT'
[Unit]
Description=Coordinated encrypted noebs recovery checkpoint
After=network-online.target tailscaled.service
Wants=network-online.target
[Service]
Type=oneshot
UMask=0077
ExecStart=/usr/bin/python3 /opt/noebs-backup/scripts/exe/coordinated_backup.py --key /opt/noebs-backup/runtime/ssh-key --machines /opt/noebs-backup/runtime/machines.json --work /var/lib/noebs-backup/checkpoints
TimeoutStartSec=1800
UNIT
cat > /etc/systemd/system/noebs-coordinated-backup.timer <<'UNIT'
[Unit]
Description=Nightly noebs checkpoint at 04:00 Dubai
[Timer]
OnCalendar=*-*-* 04:00:00 Asia/Dubai
Persistent=true
[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
'''
    ssh(key,backup,'sudo bash -c '+shlex.quote(schedule))
    if args.enable_schedule:
        lease.check()
        ssh(key,backup,'sudo systemctl enable --now noebs-coordinated-backup.timer')


if __name__=='__main__': main()
