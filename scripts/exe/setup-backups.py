#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import shlex

from reconcile import ROOT, run, ssh


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    args=parser.parse_args()
    key=args.key.resolve(); machines=json.loads(args.machines.read_text())
    data=machines['noebs-data']['ssh_destination']; backup=machines['noebs-backup']['ssh_destination']
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
Description=Retain fourteen days of hourly encrypted noebs backups
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
    unit='[Unit]\nDescription=Encrypted noebs PostgreSQL and control-plane backup\nAfter=k3s.service tailscaled.service\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/noebs-backup\n'
    timer='[Unit]\nDescription=Hourly noebs backup\n[Timer]\nOnCalendar=hourly\nPersistent=true\nRandomizedDelaySec=60\n[Install]\nWantedBy=timers.target\n'
    ssh(key,data,'sudo tee /etc/systemd/system/noebs-backup.service >/dev/null',input=unit.encode())
    ssh(key,data,'sudo tee /etc/systemd/system/noebs-backup.timer >/dev/null',input=timer.encode())
    ssh(key,data,'sudo systemctl daemon-reload; sudo systemctl enable --now noebs-backup.timer')


if __name__=='__main__': main()
