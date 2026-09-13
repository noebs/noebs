#!/usr/bin/env python3
"""Allow only this mail server's published IPv4 ports through DOCKER-USER."""
import argparse
import ipaddress
import re
import subprocess


PORTS = (25, 465, 587, 993)


class FirewallError(RuntimeError):
    """iptables could not inspect the owned rule safely."""


def rules(interface, ipv4):
    if not isinstance(interface, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,15}', interface):
        raise ValueError('An explicit Linux public interface is required')
    if not isinstance(ipv4, str):
        raise ValueError('An explicit canonical public IPv4 address is required')
    address = ipaddress.IPv4Address(ipv4)
    if not address.is_global:
        raise ValueError('An explicit public IPv4 address is required')
    return [
        ['-i', interface, '-o', 'br+', '-p', 'tcp', '-m', 'conntrack',
         '--ctorigdst', str(address), '--ctorigdstport', str(port), '--ctdir', 'ORIGINAL',
         '-m', 'comment', '--comment', 'noebs-mail-ingress-' + str(port), '-j', 'RETURN']
        for port in PORTS
    ]


def reconcile(action, interface, ipv4, run=subprocess.run):
    if action not in ('apply', 'remove'):
        raise ValueError('Unsupported mail firewall action')
    desired = rules(interface, ipv4)
    run(['iptables', '-w', '10', '-n', '-L', 'DOCKER-USER'], check=True, capture_output=True)
    def exists(rule):
        code = run(['iptables', '-w', '10', '-C', 'DOCKER-USER', *rule], capture_output=True).returncode
        if code not in (0, 1):
            raise FirewallError('Cannot inspect the managed mail firewall rule')
        return code == 0

    for rule in desired:
        present = exists(rule)
        if action == 'apply' and not present:
            # RETURN retains Docker's normal destination-port policy and every
            # other forwarding check. It bypasses only the prior public deny.
            run(['iptables', '-w', '10', '-I', 'DOCKER-USER', '1', *rule], check=True, capture_output=True)
        elif action == 'remove':
            while present:
                run(['iptables', '-w', '10', '-D', 'DOCKER-USER', *rule], check=True, capture_output=True)
                present = exists(rule)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['apply', 'remove'])
    parser.add_argument('--interface', required=True)
    parser.add_argument('--ipv4', required=True)
    args = parser.parse_args()
    reconcile(args.action, args.interface, args.ipv4)


if __name__ == '__main__':
    main()
