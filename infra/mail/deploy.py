#!/usr/bin/env python3
"""Apply or back up Stalwart on an existing host; never provision a server."""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from deployment import load_document
from runtime import InvalidMailConfiguration, render_compose, validate_runtime


class MailDeploymentError(RuntimeError):
    pass


def validate_receipt(path, config):
    path = path.absolute()
    if path.is_symlink() or not path.parent.is_dir() or path.parent.stat().st_mode & 0o077:
        raise InvalidMailConfiguration('Mail receipt requires a private parent directory (mode 0700)')
    if path.exists():
        if not path.is_file() or path.stat().st_mode & 0o077:
            raise InvalidMailConfiguration('Existing mail receipt must be a private regular file (mode 0600)')
        try:
            previous = json.loads(path.read_text())
        except (ValueError, UnicodeDecodeError):
            raise InvalidMailConfiguration('Refusing to replace an unknown mail receipt file') from None
        if (not isinstance(previous, dict) or previous.get('api_version') != 'noebs.mail.receipt/v1'
                or previous.get('ssh_destination') != config['ssh_destination']
                or previous.get('hostname') != config['hostname']):
            raise InvalidMailConfiguration('Refusing to replace an unknown or different-host mail receipt')
    return path


def write_receipt(path, config, summary):
    path = validate_receipt(path, config)
    existed = path.exists()
    receipt = {'api_version': 'noebs.mail.receipt/v1', 'ssh_destination': config['ssh_destination'],
               'hostname': config['hostname'], 'image': config['image'], 'native': summary}
    with tempfile.NamedTemporaryFile(mode='w', prefix='.mail-receipt-', dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            json.dump(receipt, output, indent=2)
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
            if existed:
                validate_receipt(path, config)
                temporary.replace(path)
            else:
                os.link(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


def ssh_command(args, config):
    command = [args.ssh, '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
               '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=2']
    if args.identity:
        command += ['-i', args.identity, '-o', 'IdentitiesOnly=yes']
    if args.known_hosts:
        command += ['-o', 'UserKnownHostsFile=' + args.known_hosts]
    script = Path(__file__).with_name('host.py').read_text()
    command += [config['ssh_destination'], 'sudo -n python3 -c ' + shlex.quote(script)]
    return command


def load_secrets(path, age_key):
    if not age_key.is_file() or age_key.stat().st_mode & 0o077:
        raise InvalidMailConfiguration('The SOPS age identity must be a private file (mode 0600)')
    result = subprocess.run(['sops', '--decrypt', '--output-type', 'json', str(path)],
                            env=os.environ | {'SOPS_AGE_KEY_FILE': str(age_key.resolve())}, capture_output=True)
    if result.returncode:
        raise InvalidMailConfiguration('Cannot decrypt the mail SOPS secrets')
    try:
        return json.loads(result.stdout)
    except (ValueError, UnicodeDecodeError):
        raise InvalidMailConfiguration('Mail SOPS secrets must decode to JSON') from None


def apply(args, config, secrets):
    validate_receipt(args.receipt, config)
    payload = {
        'action': 'apply', 'config': config, 'secrets': secrets,
        'normal_compose': render_compose(config), 'recovery_compose': render_compose(config, recovery=True),
        'native_module': Path(__file__).with_name('stalwart_config.py').read_text(),
        'firewall_module': Path(__file__).with_name('firewall.py').read_text(),
    }
    if 'webmail' in config:
        import webmail
        webmail.validate(config, secrets)
        payload['webmail_php'] = webmail.render_php(config)
        payload['webmail_php_ini'] = webmail.render_php_ini()
    result = subprocess.run(ssh_command(args, config), input=json.dumps(payload).encode(), capture_output=True)
    if result.returncode:
        # The remote script sanitizes failures. Do not propagate native output
        # or decrypted configuration from a failing third-party command.
        raise MailDeploymentError('Mail setup failed; inspect the private Stalwart runtime on the configured host')
    response = json.loads(result.stdout)
    if response.get('status') != 'applied':
        raise MailDeploymentError('The mail host did not return an applied configuration receipt')
    if not isinstance(response.get('mail'), dict):
        raise MailDeploymentError('The mail host did not return its native configuration summary')
    write_receipt(args.receipt, config, response['mail'])
    print('Stalwart runtime and domain configuration applied; verify DNS and TLS before changing mail routing.')


def backup(args, config):
    if not re.fullmatch(r'age1[023456789acdefghjklmnpqrstuvwxyz]{58}', args.recipient or ''):
        raise InvalidMailConfiguration('Backup requires an explicit age public recipient')
    output = args.output.resolve()
    if output.exists() or not output.parent.is_dir() or output.parent.stat().st_mode & 0o077:
        raise InvalidMailConfiguration('Backup output must be a new file in a private directory (mode 0700)')
    if not shutil.which('age'):
        raise InvalidMailConfiguration('The age encryption command is required for mail backups')
    # SSH carries the archive directly into age; mailbox plaintext never lands
    # in a local temporary file. A failed operation never leaves a success file.
    temporary = output.with_name(output.name + '.incoming')
    remote = encrypt = None
    created = False
    try:
        with temporary.open('xb') as destination:
            created = True
            temporary.chmod(0o600)
            remote = subprocess.Popen(ssh_command(args, config), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                      stderr=subprocess.DEVNULL)
            encrypt = subprocess.Popen(['age', '--encrypt', '--recipient', args.recipient],
                                       stdin=remote.stdout, stdout=destination, stderr=subprocess.DEVNULL)
            remote.stdout.close()
            remote.stdin.write(json.dumps({'action': 'backup', 'config': config}).encode())
            remote.stdin.close()
            encryption_status = encrypt.wait()
            remote_status = remote.wait()
            if encryption_status or remote_status:
                raise MailDeploymentError('Encrypted mail backup failed; no completed archive was published')
            destination.flush()
            os.fsync(destination.fileno())
        os.link(temporary, output)
    finally:
        for process in [encrypt, remote]:
            if process is not None and process.poll() is None:
                process.terminate()
                process.wait(timeout=30)
        if created:
            temporary.unlink(missing_ok=True)
    print('Encrypted mail backup: ' + str(output))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['check', 'apply', 'backup'])
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--secrets', type=Path, help='SOPS encrypted native mail credentials; required for check/apply')
    parser.add_argument('--age-key', type=Path, help='Private age identity for the SOPS input')
    parser.add_argument('--ssh', default='ssh', help='SSH client or existing Tailscale SSH wrapper')
    parser.add_argument('--identity', help='Explicit SSH identity; omit to use the client agent or Tailscale SSH')
    parser.add_argument('--known-hosts', help='Explicit known_hosts file in the SSH client filesystem')
    parser.add_argument('--recipient', help='Public age recipient for a backup')
    parser.add_argument('--output', type=Path, help='New encrypted backup archive in a private directory')
    parser.add_argument('--receipt', type=Path, help='Private applied-state receipt with public DKIM records; required for apply')
    args = parser.parse_args()
    os.umask(0o077)
    config = validate_runtime(load_document(args.config))
    if args.action == 'backup':
        if args.output is None:
            parser.error('backup requires --output')
        backup(args, config)
        return
    if args.secrets is None or args.age_key is None:
        parser.error('check and apply require --secrets and --age-key')
    if args.action == 'apply' and args.receipt is None:
        parser.error('apply requires --receipt')
    import stalwart_config
    secrets = load_secrets(args.secrets, args.age_key)
    stalwart_config.validate(config, secrets)
    if 'webmail' in config:
        import webmail
        webmail.validate(config, secrets)
    stalwart_config.render_startup(config, secrets)
    render_compose(config)
    if args.action == 'check':
        print('Mail runtime and native configuration validated; host unchanged.')
    else:
        apply(args, config, secrets)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # Secrets and mailbox data must not appear in a failed setup transcript.
        message = str(error) if isinstance(error, (InvalidMailConfiguration, MailDeploymentError)) else 'Mail configuration or maintenance failed'
        print(message, file=sys.stderr)
        sys.exit(1)
