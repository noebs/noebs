#!/usr/bin/env python3
"""Reconcile the mail server's explicit Cloudflare records, preserving web DNS."""
import argparse
import base64
import binascii
import ipaddress
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

class InvalidMailDNS(ValueError):
    pass


SELECTOR = re.compile(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?')


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise InvalidMailDNS('Mail JSON objects require distinct keys')
        result[key] = value
    return result


def txt_content(value):
    if not isinstance(value, str):
        raise InvalidMailDNS('DNS TXT content must be a string')
    if value.startswith('"'):
        try:
            return ''.join(shlex.split(value))
        except ValueError:
            raise InvalidMailDNS('DNS TXT quoting is malformed') from None
    return value


def txt_tags(value):
    tags = {}
    for part in txt_content(value).split(';'):
        if not part.strip():
            continue
        key, separator, content = part.partition('=')
        key = key.strip().lower()
        if not separator or not re.fullmatch(r'[a-z][a-z0-9_]*', key) or key in tags:
            raise InvalidMailDNS('DNS authentication TXT tags must be valid and distinct')
        tags[key] = content.strip()
    return tags


def validate_dkim(selector, content):
    if not isinstance(selector, str) or not SELECTOR.fullmatch(selector):
        raise InvalidMailDNS('DKIM selector must be one canonical DNS label')
    tags = txt_tags(content)
    if tags.get('v') != 'DKIM1' or tags.get('k') != 'rsa' or not tags.get('p'):
        raise InvalidMailDNS('DKIM requires a nonempty native RSA public key with an explicit version')
    try:
        key = base64.b64decode(tags['p'], validate=True)
    except (ValueError, binascii.Error):
        raise InvalidMailDNS('DKIM public key must be valid base64') from None
    if len(key) < 256:
        raise InvalidMailDNS('DKIM public key is too short for the managed 2048-bit RSA signer')
    return tags


def is_dkim_record(content):
    content = txt_content(content).strip()
    if re.match(r'v\s*=\s*DKIM1\s*(?:;|$)', content, re.IGNORECASE):
        return True
    try:
        tags = txt_tags(content)
    except InvalidMailDNS:
        return False
    return 'v' not in tags and 'p' in tags and tags.get('k', 'rsa') == 'rsa'


def dkim_from_receipt(receipt, config):
    if not isinstance(receipt, dict) or receipt.get('api_version') != 'noebs.mail.receipt/v1':
        raise InvalidMailDNS('DNS publication requires a generated mail deployment receipt')
    for field in ['ssh_destination', 'hostname', 'image']:
        if receipt.get(field) != config[field]:
            raise InvalidMailDNS('Mail receipt does not match the configured host and image')
    native = receipt.get('native')
    domains = config['domains']
    if (not isinstance(native, dict) or not isinstance(native.get('domains'), list)
            or any(not isinstance(domain, str) for domain in native['domains'])
            or len(native['domains']) != len(domains) or set(native['domains']) != set(domains)):
        raise InvalidMailDNS('Mail receipt must contain the exact configured domain inventory')
    records = native.get('dkim_records')
    if not isinstance(records, list) or len(records) != len(domains):
        raise InvalidMailDNS('Mail receipt requires one current DKIM record for each domain')
    selector = config.get('dkim_selector')
    if not isinstance(selector, str) or not SELECTOR.fullmatch(selector):
        raise InvalidMailDNS('The configuration requires an explicit canonical dkim_selector')
    result = {}
    for record in records:
        if not isinstance(record, dict) or set(record) != {'domain', 'selector', 'name', 'type', 'content'}:
            raise InvalidMailDNS('Mail receipt has an invalid native DKIM record shape')
        domain = record['domain']
        if not isinstance(domain, str) or domain not in domains or domain in result:
            raise InvalidMailDNS('Mail receipt has an unknown or duplicate DKIM domain')
        if (record['selector'] != selector or record['type'] != 'TXT'
                or record['name'] != selector + '._domainkey.' + domain):
            raise InvalidMailDNS('Mail receipt DKIM names must match the configured current selector')
        validate_dkim(selector, record['content'])
        result[domain] = [{'selector': selector, 'content': record['content']}]
    return result


class Cloudflare:
    def __init__(self, token):
        if not isinstance(token, str) or not token or any(character.isspace() for character in token):
            raise InvalidMailDNS('An explicit Cloudflare API token is required')
        self.token = token

    def request(self, method, path, data=None):
        request = Request('https://api.cloudflare.com/client/v4' + path, method=method,
                          data=json.dumps(data).encode() if data is not None else None,
                          headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        try:
            with urlopen(request, timeout=20) as response:
                body = json.load(response)
        except HTTPError as error:
            raise InvalidMailDNS('Cloudflare request failed with HTTP ' + str(error.code)) from None
        except (URLError, ValueError):
            raise InvalidMailDNS('Cloudflare request failed or returned invalid JSON') from None
        if not body.get('success'):
            raise InvalidMailDNS('Cloudflare request failed: ' + ','.join(str(error.get('code')) for error in body.get('errors', [])))
        return body

    def zone(self, name):
        result = self.request('GET', '/zones?' + urlencode({'name': name}))['result']
        if len(result) != 1 or result[0]['name'] != name or result[0]['status'] != 'active':
            raise InvalidMailDNS('Expected one active Cloudflare zone for ' + name)
        return result[0]['id']

    def records(self, zone_id):
        result = []
        page = 1
        while True:
            body = self.request('GET', '/zones/' + zone_id + '/dns_records?' + urlencode({'per_page': 500, 'page': page}))
            result.extend(body['result'])
            if page >= body.get('result_info', {}).get('total_pages', 1):
                return result
            page += 1


def desired_records(hostname, ipv4, domains, ttl, dkim=None, hostname_only=False, webmail_hostname=None):
    from runtime import HOSTNAME
    if (not isinstance(domains, list) or not domains
            or any(not isinstance(domain, str) or not HOSTNAME.fullmatch(domain) for domain in domains)
            or len(set(domains)) != len(domains)):
        raise InvalidMailDNS('Explicit unique mail domains are required')
    if not isinstance(hostname, str) or not HOSTNAME.fullmatch(hostname):
        raise InvalidMailDNS('Mail hostname must be a canonical DNS name')
    try:
        address = ipaddress.IPv4Address(ipv4)
    except (TypeError, ValueError):
        raise InvalidMailDNS('Mail DNS requires a public IPv4 address') from None
    if not isinstance(ipv4, str) or str(address) != ipv4 or not address.is_global:
        raise InvalidMailDNS('Mail DNS requires a canonical public IPv4 address')
    if type(ttl) is not int or not 60 <= ttl <= 86400:
        raise InvalidMailDNS('DNS TTL must be an explicit integer between 60 and 86400 seconds')
    owners = [domain for domain in domains if hostname.endswith('.' + domain)]
    if len(owners) != 1:
        raise InvalidMailDNS('Mail hostname must belong to exactly one configured domain')
    records = {domain: [] for domain in domains}
    records[owners[0]].append({'type': 'A', 'name': hostname, 'content': ipv4, 'ttl': ttl, 'proxied': False})
    if webmail_hostname is not None:
        if (not isinstance(webmail_hostname, str) or not HOSTNAME.fullmatch(webmail_hostname)
                or webmail_hostname == hostname):
            raise InvalidMailDNS('Webmail DNS requires a distinct canonical hostname')
        webmail_owners = [domain for domain in domains if webmail_hostname.endswith('.' + domain)]
        if len(webmail_owners) != 1:
            raise InvalidMailDNS('Webmail hostname must belong to exactly one configured domain')
        records[webmail_owners[0]].append({'type': 'A', 'name': webmail_hostname, 'content': ipv4,
                                         'ttl': ttl, 'proxied': False})
    if hostname_only:
        return {domain: values for domain, values in records.items() if values}
    if not isinstance(dkim, dict) or set(dkim) != set(domains):
        raise InvalidMailDNS('Full DNS publication requires generated DKIM records for every mail domain')
    for domain in domains:
        records[domain].extend([
            {'type': 'MX', 'name': domain, 'content': hostname, 'priority': 10, 'ttl': ttl},
            {'type': 'TXT', 'name': domain, 'content': 'v=spf1 ip4:' + ipv4 + ' -all', 'ttl': ttl},
            {'type': 'TXT', 'name': '_dmarc.' + domain, 'content': 'v=DMARC1; p=reject; rua=mailto:postmaster@' + domain, 'ttl': ttl},
        ])
        if not isinstance(dkim[domain], list) or not dkim[domain]:
            raise InvalidMailDNS('Each domain requires at least one active DKIM selector')
        selectors = set()
        for entry in dkim[domain]:
            if (not isinstance(entry, dict) or set(entry) != {'selector', 'content'}
                    or not isinstance(entry['selector'], str) or not entry['selector']
                    or not isinstance(entry['content'], str) or not entry['content'].startswith('v=DKIM1;')
                    or not any(part.strip().startswith('p=') and part.strip()[2:] for part in entry['content'].split(';'))):
                raise InvalidMailDNS('DKIM requires the actual selector and public DNS TXT value from Stalwart')
            validate_dkim(entry['selector'], entry['content'])
            if entry['selector'] in selectors:
                raise InvalidMailDNS('DKIM selectors must be distinct for each domain')
            selectors.add(entry['selector'])
            records[domain].append({'type': 'TXT', 'name': entry['selector'] + '._domainkey.' + domain,
                                    'content': entry['content'], 'ttl': ttl})
    return records


def matches_scope(record, wanted):
    if record['type'] != wanted['type'] or record['name'].rstrip('.').lower() != wanted['name']:
        return False
    if wanted['type'] == 'TXT' and wanted['content'].startswith('v=spf1 '):
        content = txt_content(record['content']).lower()
        return content == 'v=spf1' or content.startswith(('v=spf1 ', 'v=spf1\t'))
    if wanted['type'] == 'TXT' and wanted['content'].startswith('v=DMARC1;'):
        return bool(re.match(r'v\s*=\s*DMARC1\s*(?:;|$)', txt_content(record['content']).strip(), re.IGNORECASE))
    if wanted['type'] == 'TXT' and wanted['content'].startswith('v=DKIM1;'):
        return is_dkim_record(record['content'])
    return True


def equivalent(record, wanted):
    for name, value in wanted.items():
        current = record.get(name)
        if name == 'content' and wanted['type'] in ('MX', 'CNAME'):
            current = str(current).rstrip('.').lower()
        if name == 'content' and wanted['type'] == 'TXT':
            current = txt_content(current)
        if current != value:
            return False
    return True


def plan_records(existing, desired, zone_name):
    from runtime import HOSTNAME
    if not isinstance(zone_name, str) or not HOSTNAME.fullmatch(zone_name):
        raise InvalidMailDNS("An explicit canonical Cloudflare zone is required")
    actions = []
    for wanted in desired:
        conflicts = [record for record in existing if record['name'].rstrip('.').lower() == wanted['name'] and record['type'] == 'CNAME']
        # Cloudflare always flattens apex CNAMEs to addresses, allowing the
        # website alias to coexist with mail MX/TXT records at that apex.
        apex_mail_record = wanted['name'] == zone_name and wanted['type'] in ('MX', 'TXT')
        if conflicts and not apex_mail_record:
            raise InvalidMailDNS('Mail DNS conflicts with an existing CNAME at ' + wanted['name'])
        if wanted['type'] == 'A':
            # This runtime deliberately listens and delivers over IPv4 only.
            # Remove stale IPv6 routing at the owned mail hostname, while
            # leaving website and other hosts' AAAA records untouched.
            for record in existing:
                if record['type'] == 'AAAA' and record['name'].rstrip('.').lower() == wanted['name']:
                    actions.append({'method': 'DELETE', 'id': record['id'],
                                    'record': {key: record[key] for key in ('type', 'name', 'content')}})
        current = [record for record in existing if matches_scope(record, wanted)]
        if not current:
            actions.append({'method': 'POST', 'record': wanted})
        else:
            primary = next((record for record in current if equivalent(record, wanted)), current[0])
            if not equivalent(primary, wanted):
                actions.append({'method': 'PUT', 'id': primary['id'], 'record': wanted})
            for record in current:
                if record['id'] != primary['id']:
                    actions.append({'method': 'DELETE', 'id': record['id'], 'record': {key: record[key] for key in ('type', 'name', 'content')}})
    return actions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--secrets', type=Path, required=True, help='SOPS encrypted mail credentials')
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument('--receipt', type=Path, help='Applied mail receipt containing native DKIM records')
    selection.add_argument('--hostname-only', action='store_true')
    parser.add_argument('--ttl', type=int, default=300)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if not 60 <= args.ttl <= 86400:
        parser.error('--ttl must be between 60 and 86400 seconds')
    from deploy import load_document
    config = load_document(args.config)
    from runtime import validate_runtime
    validate_runtime(config)
    webmail_hostname = config['webmail']['hostname'] if 'webmail' in config else None
    desired_records(config['hostname'], config['public_ipv4'], config['domains'], args.ttl,
                    hostname_only=True, webmail_hostname=webmail_hostname)
    dkim = None
    if args.receipt:
        try:
            receipt = json.loads(args.receipt.read_text(), object_pairs_hook=unique_object)
        except (ValueError, UnicodeDecodeError):
            raise InvalidMailDNS('Cannot parse the mail deployment receipt') from None
        dkim = dkim_from_receipt(receipt, config)
    desired = desired_records(config['hostname'], config['public_ipv4'], config['domains'], args.ttl,
                              dkim, args.hostname_only, webmail_hostname)
    decrypted = subprocess.run(['sops', '--decrypt', '--output-type', 'json', str(args.secrets)], capture_output=True)
    if decrypted.returncode:
        raise InvalidMailDNS('Cannot decrypt the mail DNS credentials')
    try:
        secrets = json.loads(decrypted.stdout, object_pairs_hook=unique_object)
    except (ValueError, UnicodeDecodeError):
        raise InvalidMailDNS('Mail DNS credentials must decode to a valid JSON object') from None
    if not isinstance(secrets, dict):
        raise InvalidMailDNS('Mail DNS credentials must decode to a JSON object')
    client = Cloudflare(secrets.get('cloudflare_api_token'))
    zones = {}
    plans = {}
    for domain, records in desired.items():
        zones[domain] = client.zone(domain)
        plans[domain] = plan_records(client.records(zones[domain]), records, domain)
    print(json.dumps({'apply': args.apply, 'changes': plans}, indent=2), flush=True)
    if args.apply:
        for domain, actions in plans.items():
            for action in actions:
                path = '/zones/' + zones[domain] + '/dns_records'
                if 'id' in action:
                    path += '/' + action['id']
                client.request(action['method'], path, None if action['method'] == 'DELETE' else action['record'])
        for domain, records in desired.items():
            if plan_records(client.records(zones[domain]), records, domain):
                raise InvalidMailDNS('Cloudflare records did not converge for ' + domain)
        print('Mail DNS records verified against Cloudflare.')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except InvalidMailDNS as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except Exception:
        print('Mail DNS configuration, credentials or provider request failed', file=sys.stderr)
        sys.exit(1)
