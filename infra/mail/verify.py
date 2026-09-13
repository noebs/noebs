#!/usr/bin/env python3
"""Check public mail DNS and protocol behavior without delivering a message."""
import argparse
import imaplib
import ipaddress
import json
import shlex
import smtplib
import ssl
import subprocess
import sys

from dns import InvalidMailDNS, SELECTOR, is_dkim_record, txt_tags, validate_dkim


class MailReadinessError(ValueError):
    """A required DNS record or protocol property is missing."""


def dns_records(name, kind):
    result = subprocess.run(
        ['dig', '+time=3', '+tries=1', '+short', kind, name],
        capture_output=True, text=True, timeout=5, check=True,
    )
    records = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    if kind == 'TXT':
        return [''.join(shlex.split(line)) for line in records]
    return records


def require(condition, message):
    if not condition:
        raise MailReadinessError(message)


def check_host_dns(hostname, ipv4, query=dns_records):
    require(query(hostname, 'A') == [ipv4], 'Mail hostname must resolve directly to the configured IPv4 address')
    require(not query(hostname, 'AAAA'), 'IPv4-only mail host must not publish an IPv6 address')
    reverse = ipaddress.IPv4Address(ipv4).reverse_pointer
    require([value.rstrip('.').lower() for value in query(reverse, 'PTR')] == [hostname],
            'Provider PTR must match the mail hostname')


def check_domain_dns(domain, hostname, ipv4, query=dns_records):
    mx = query(domain, 'MX')
    targets = []
    for value in mx:
        parts = value.split()
        require(len(parts) == 2 and parts[0].isdigit(), 'Malformed MX record')
        targets.append(parts[1].rstrip('.').lower())
    require(targets == [hostname], 'Domain MX must route to this mail server')
    spf = [value for value in query(domain, 'TXT') if value.lower().startswith('v=spf1')]
    require(len(spf) == 1, 'Domain must have exactly one SPF policy')
    terms = spf[0].lower().split()
    require('ip4:' + ipv4 in terms or '+ip4:' + ipv4 in terms or 'mx' in terms or '+mx' in terms,
            'SPF must authorize the configured server address or its MX')
    require('-all' in terms or '~all' in terms, 'SPF must end with a restrictive all mechanism')
    require(terms[-1] in ('-all', '~all'), 'SPF all mechanism must be last')
    require(len(terms) == 3 and terms[0] == 'v=spf1'
            and terms[1] in ('ip4:' + ipv4, '+ip4:' + ipv4, 'mx', '+mx'),
            'SPF must authorize this mail server without contradictory or unsupported mechanisms')
    dmarc = [value for value in query('_dmarc.' + domain, 'TXT') if value.lower().startswith('v=dmarc1;')]
    require(len(dmarc) == 1, 'Domain must have exactly one DMARC policy')
    try:
        policies = txt_tags(dmarc[0])
    except InvalidMailDNS as error:
        raise MailReadinessError(str(error)) from None
    require(policies.get('v') == 'DMARC1' and policies.get('p') in ('none', 'quarantine', 'reject'),
            'DMARC requires an explicit valid version and policy')


def check_dkim(domain, selector, query=dns_records):
    values = query(selector + '._domainkey.' + domain, 'TXT')
    keys = []
    for value in values:
        if is_dkim_record(value):
            try:
                keys.append(validate_dkim(selector, value))
            except InvalidMailDNS as error:
                raise MailReadinessError(str(error)) from None
    require(len(keys) == 1, 'DKIM selector must publish one nonempty public key')


def smtp_features(client):
    code, _ = client.ehlo()
    require(code == 250, 'SMTP EHLO failed')
    return client.esmtp_features


def check_relay_denied(client):
    # Reserved example domains, no DATA command, and no message delivery.
    code, response = client.mail('readiness-probe@example.org')
    if code == 530 or (code == 503 and response.strip() == b'5.5.1 You must authenticate first.'):
        return
    require(code == 250, 'SMTP MAIL did not allow testing the relay policy')
    code, response = client.rcpt('readiness-probe@example.net')
    require(code == 530 or (code == 550 and response.strip() == b'5.1.2 Relay not allowed.'),
            'Unauthenticated external recipient was not rejected by the relay policy')
    client.rset()


def check_smtp(hostname, port, context=None, timeout=10):
    context = context or ssl.create_default_context()
    if port == 465:
        with smtplib.SMTP_SSL(hostname, port, timeout=timeout, context=context) as client:
            features = smtp_features(client)
            require('auth' in features, 'TLS submission does not advertise authentication')
            check_relay_denied(client)
        return
    with smtplib.SMTP(hostname, port, timeout=timeout) as client:
        features = smtp_features(client)
        require('starttls' in features, 'SMTP does not advertise STARTTLS')
        require('auth' not in features, 'SMTP advertises password authentication before TLS')
        client.starttls(context=context)
        features = smtp_features(client)
        if port == 587:
            require('auth' in features, 'Submission does not advertise authentication after TLS')
        else:
            require('auth' not in features, 'Public MX listener must not offer submission authentication')
        check_relay_denied(client)


def check_imap(hostname, context=None, timeout=10):
    client = imaplib.IMAP4_SSL(hostname, port=993, ssl_context=context or ssl.create_default_context(), timeout=timeout)
    try:
        status, capabilities = client.capability()
        require(status == 'OK' and any(b'IMAP4' in entry.upper() for entry in capabilities),
                'IMAPS capability check failed')
    finally:
        client.logout()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--hostname', required=True)
    parser.add_argument('--ipv4', required=True, type=ipaddress.IPv4Address)
    parser.add_argument('--domain', required=True, action='append')
    parser.add_argument('--dkim', action='append', required=True, metavar='DOMAIN:SELECTOR',
                        help='Repeat for every active signing selector reported by Stalwart')
    parser.add_argument('--dns-only', action='store_true')
    args = parser.parse_args()
    from runtime import HOSTNAME
    if not HOSTNAME.fullmatch(args.hostname) or any(not HOSTNAME.fullmatch(domain) for domain in args.domain):
        parser.error('hostname and domains must be canonical DNS names')
    if len(set(args.domain)) != len(args.domain) or len(set(args.dkim)) != len(args.dkim):
        parser.error('domains and DKIM selectors must be unique')
    covered_domains = set()
    checks = [('host-dns', lambda: check_host_dns(args.hostname, str(args.ipv4)))]
    for domain in args.domain:
        checks.append(('dns:' + domain, lambda domain=domain: check_domain_dns(domain, args.hostname, str(args.ipv4))))
    for value in args.dkim:
        if value.count(':') != 1 or not all(value.split(':')):
            parser.error('--dkim requires DOMAIN:SELECTOR')
        domain, selector = value.split(':')
        if domain not in args.domain or not SELECTOR.fullmatch(selector):
            parser.error('--dkim requires a configured domain and one canonical selector label')
        covered_domains.add(domain)
        checks.append(('dkim:' + value, lambda domain=domain, selector=selector: check_dkim(domain, selector)))
    if covered_domains != set(args.domain):
        parser.error('every configured domain requires an explicit --dkim selector')
    if not args.dns_only:
        for port in (25, 465, 587):
            checks.append(('smtp:' + str(port), lambda port=port: check_smtp(args.hostname, port)))
        checks.append(('imap:993', lambda: check_imap(args.hostname)))
    results = []
    for name, check in checks:
        try:
            check()
            results.append({'check': name, 'ok': True})
        except (ValueError, OSError, smtplib.SMTPException, imaplib.IMAP4.error, subprocess.SubprocessError) as error:
            results.append({'check': name, 'ok': False, 'error': str(error)})
    print(json.dumps({'checks': results, 'messages_sent': 0}, indent=2))
    return 0 if all(result['ok'] for result in results) else 1


if __name__ == '__main__':
    sys.exit(main())
