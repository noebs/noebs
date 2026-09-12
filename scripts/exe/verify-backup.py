#!/usr/bin/env python3
"""Restore encrypted PostgreSQL dumps into isolated, temporary local clusters."""
import argparse
import gzip
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--archives', type=Path, required=True)
    parser.add_argument('--age', type=Path, required=True)
    parser.add_argument('--age-key', type=Path, required=True)
    parser.add_argument('--postgres-bin', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--prepare-event-replay', action='store_true')
    args = parser.parse_args()
    os.umask(0o077)
    binaries = args.postgres_bin.resolve()
    reports = []
    for authority in ['postgres', 'temporal', 'keycloak']:
        matches = list(args.archives.glob('*' + authority + '.sql.gz.age'))
        if len(matches) != 1:
            raise ValueError('Expected one encrypted archive for ' + authority)
        archive = matches[0]
        compressed = subprocess.check_output([str(args.age.resolve()), '--decrypt', '-i',
                                             str(args.age_key.resolve()), str(archive)])
        sql = gzip.decompress(compressed)
        if b'PostgreSQL database cluster dump complete' not in sql:
            raise ValueError('Incomplete PostgreSQL dump: ' + authority)
        with tempfile.TemporaryDirectory(prefix='restore-', dir=args.work.resolve()) as directory:
            work = Path(directory)
            data = work / 'data'
            log_path = work / 'restore.log'
            with log_path.open('wb') as log:
                def run(command, **kwargs):
                    return subprocess.run(command, check=True, stdout=log, stderr=log, **kwargs)
                try:
                    run([str(binaries / 'initdb'), '-D', str(data), '--no-locale', '--encoding=UTF8',
                         '--auth=trust', '--username=noebs_backup_verifier'])
                    run([str(binaries / 'pg_ctl'), '-D', str(data), '-l', str(work / 'server.log'),
                         '-o', '-c listen_addresses= -c unix_socket_directories=' + str(work), '-w', 'start'])
                    try:
                        run([str(binaries / 'psql'), '-X', '-h', str(work), '-U', 'noebs_backup_verifier',
                             '-d', 'postgres', '--set=ON_ERROR_STOP=1'], input=sql)
                        if authority == 'postgres' and args.prepare_event_replay:
                            for database in ['identity_auth', 'wallet_ledger', 'ebs_adapter']:
                                reset = (Path(__file__).parent / 'recovery' / (database + '.sql')).read_bytes()
                                run([str(binaries / 'psql'), '-X', '-h', str(work), '-U', 'noebs_backup_verifier',
                                     '-d', database, '--set=ON_ERROR_STOP=1'], input=reset)
                        count = subprocess.check_output([str(binaries / 'psql'), '-X', '-At', '-h', str(work),
                                                        '-U', 'noebs_backup_verifier', '-d', 'postgres', '-c',
                                                        'SELECT count(*) FROM pg_database WHERE datallowconn AND NOT datistemplate'])
                    finally:
                        run([str(binaries / 'pg_ctl'), '-D', str(data), '-m', 'immediate', '-w', 'stop'])
                except subprocess.CalledProcessError:
                    # SQL errors can include row data; keep diagnostic output private.
                    failure = args.work / (authority + '-restore-failure.log')
                    failure.write_bytes(log_path.read_bytes())
                    raise RuntimeError('Restore failed for ' + authority + '; private diagnostic: ' + str(failure)) from None
            reports.append({'authority': authority, 'databases': int(count), 'restored': True})
    print(json.dumps({'restores': reports}, sort_keys=True))


if __name__ == '__main__':
    main()
