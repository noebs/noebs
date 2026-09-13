"""Stalwart v0.16.21 native registry reconciliation (stdlib plus host OpenSSL).

Public config extends runtime.py with domains (explicit DNS names), mailboxes
[{email, name, quota_bytes, role: User|Admin, password_policy: Managed|Initial}],
and dkim_selector. Secrets are
{admin_password, mailbox_passwords: {email: password}, cloudflare_api_token}.
All validation happens before API calls. Secrets never appear in diagnostics.
The startup file contains only the native DataStore. Other state, mailbox data,
DKIM keys and the ACME account remain in the persistent native registry.

API verified against release tag v0.16.21, crates/registry/src/schema/structs.rs
and crates/jmap/src/registry; see https://stalw.art/docs/configuration/declarative-deployments/.
"""
import base64
import hashlib
import ipaddress
import json
import re
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


class ConfigurationError(ValueError):
    """Invalid explicit deployment configuration; no mutation has occurred."""


class ReconciliationError(RuntimeError):
    """Native registry refused a request, with secret-safe diagnostics."""


DOMAIN = re.compile(r'(?=.{1,253}\Z)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\Z')
LOCAL = re.compile(r'[a-z0-9][a-z0-9._+-]{0,63}\Z')


def validate(config, secrets):
    if not isinstance(config, dict) or not isinstance(secrets, dict):
        raise ConfigurationError('Configuration and secrets must be objects')
    for key in ('hostname', 'public_ipv4', 'domains', 'mailboxes', 'dkim_selector'):
        if key not in config:
            raise ConfigurationError('Missing configuration field: ' + key)
    if not isinstance(config['hostname'], str) or not DOMAIN.fullmatch(config['hostname']):
        raise ConfigurationError('hostname must be a canonical DNS name')
    try:
        address = ipaddress.IPv4Address(config['public_ipv4'])
        if not address.is_global:
            raise ValueError()
    except (ValueError, TypeError):
        raise ConfigurationError('public_ipv4 must be a public IPv4 address') from None
    domains = config['domains']
    if (not isinstance(domains, list) or not domains or
            any(not isinstance(d, str) or not DOMAIN.fullmatch(d) for d in domains) or
            len(set(domains)) != len(domains)):
        raise ConfigurationError('domains must contain unique canonical DNS names')
    if config['hostname'] in domains or not any(config['hostname'].endswith('.' + d) for d in domains):
        raise ConfigurationError('hostname must be a subdomain of a managed mail domain')
    if not isinstance(config['dkim_selector'], str) or not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,62}', config['dkim_selector']):
        raise ConfigurationError('dkim_selector must be an explicit DNS label')
    mailboxes = config['mailboxes']
    if not isinstance(mailboxes, list) or not mailboxes:
        raise ConfigurationError('mailboxes must be an explicit nonempty list')
    addresses = set()
    for box in mailboxes:
        if not isinstance(box, dict) or set(box) != {'email', 'name', 'quota_bytes', 'role', 'password_policy'}:
            raise ConfigurationError('Each mailbox requires email, name, quota_bytes, role and password_policy')
        email = box['email']
        if not isinstance(email, str) or email.count('@') != 1:
            raise ConfigurationError('Mailbox email must be a canonical address')
        local, domain = email.split('@')
        if not LOCAL.fullmatch(local) or domain not in domains or email in addresses:
            raise ConfigurationError('Mailbox email must be unique and belong to a managed domain')
        addresses.add(email)
        if (not isinstance(box['name'], str) or not box['name'].strip() or
                any(ord(ch) < 32 for ch in box['name']) or len(box['name']) > 200):
            raise ConfigurationError('Mailbox name must be explicit printable text')
        if type(box['quota_bytes']) is not int or box['quota_bytes'] < 1048576:
            raise ConfigurationError('Mailbox quota_bytes must be an integer of at least one MiB')
        if box['password_policy'] not in ('Managed', 'Initial'):
            raise ConfigurationError('Mailbox password_policy must be Managed or Initial')
        if box['role'] not in ('User', 'Admin'):
            raise ConfigurationError('Mailbox role must be User or Admin')
    if not any(box['role'] == 'Admin' for box in mailboxes):
        raise ConfigurationError('An explicit native administrator mailbox is required')
    if any('postmaster@' + domain not in addresses for domain in domains):
        raise ConfigurationError('Every mail domain requires a postmaster mailbox')
    passwords = secrets.get('mailbox_passwords')
    if not isinstance(passwords, dict) or set(passwords) != addresses:
        raise ConfigurationError('mailbox_passwords must match the configured mailbox addresses')
    for value in [secrets.get('admin_password'), secrets.get('cloudflare_api_token'), *passwords.values()]:
        if not isinstance(value, str) or len(value) < 24 or any(ord(c) < 33 or ord(c) > 126 for c in value):
            raise ConfigurationError('Every secret must contain at least 24 printable non-space ASCII characters')
    if len(set(passwords.values())) != len(passwords) or secrets['admin_password'] in passwords.values():
        raise ConfigurationError('Each mailbox and recovery administrator requires a distinct password')


def render_startup(config, secrets):
    validate(config, secrets)
    return json.dumps({'@type': 'RocksDb', 'path': '/var/lib/stalwart/data'}, indent=2) + '\n'


class NativeClient:
    def __init__(self, base_url, password):
        url = urllib.parse.urlsplit(base_url)
        if url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port or url.path not in ('', '/') or url.query or url.fragment or url.username:
            raise ConfigurationError('Management endpoint must be an explicit loopback HTTP port')
        self.url = base_url.rstrip('/') + '/jmap/'
        self.authorization = 'Basic ' + base64.b64encode(('admin:' + password).encode()).decode()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, kind, operation, arguments):
        method = 'x:' + kind + '/' + operation
        data = json.dumps({'using': ['urn:ietf:params:jmap:core', 'urn:stalwart:jmap'],
                           'methodCalls': [[method, arguments, 'apply']]}).encode()
        request = urllib.request.Request(self.url, data=data, headers={
            'Authorization': self.authorization, 'Content-Type': 'application/json'})
        try:
            with self.opener.open(request, timeout=90) as response:
                body = json.load(response)
        except (urllib.error.URLError, ValueError, TimeoutError):
            raise ReconciliationError('Native registry request failed: ' + method) from None
        responses = body.get('methodResponses', [])
        if len(responses) != 1 or len(responses[0]) != 3 or responses[0][0] != method:
            raise ReconciliationError('Native registry rejected method: ' + method)
        result = responses[0][1]
        failures = [name for name in ('notCreated', 'notUpdated', 'notDestroyed') if result.get(name)]
        if failures:
            # Property names are safe; server descriptions may echo a supplied secret.
            properties = sorted({p for field in failures for error in result[field].values()
                                 for p in error.get('properties', []) + [v.get('property') for v in error.get('validationErrors', [])]
                                 if isinstance(p, str)})
            raise ReconciliationError('Native registry rejected ' + method + ' properties: ' + ', '.join(properties))
        return result

    def objects(self, kind):
        return self.call(kind, 'get', {})['list']

    def singleton(self, kind, desired):
        old = self.call(kind, 'get', {'ids': ['singleton']})['list'][0]
        patch = {k: v for k, v in desired.items() if old.get(k) != v}
        if patch:
            self.call(kind, 'set', {'update': {'singleton': patch}})
        return 'singleton'

    def upsert(self, kind, identity, desired, create_only=None):
        matches = [obj for obj in self.objects(kind) if all(obj.get(k) == v for k, v in identity.items())]
        if len(matches) > 1:
            raise ReconciliationError('Duplicate managed native object: ' + kind)
        if matches:
            old = matches[0]
            patch = {k: v for k, v in desired.items() if old.get(k) != v}
            if kind == 'Account' and 'credentials' in desired:
                # Patch only the primary secret: native IDs, enrolled OTP and
                # independent app passwords belong to the persistent account.
                primary = [(key, item) for key, item in old.get('credentials', {}).items()
                           if item.get('@type') == 'Password']
                if len(primary) > 1:
                    raise ReconciliationError('Managed account has ambiguous primary credentials')
                if primary:
                    key, _ = primary[0]
                    patch.pop('credentials', None)
                    patch['credentials/' + key + '/secret'] = desired['credentials']['0']['secret']
            if patch:
                self.call(kind, 'set', {'update': {old['id']: patch}})
            return old['id']
        obj = dict(desired)
        if create_only:
            obj.update(create_only())
        result = self.call(kind, 'set', {'create': {'managed': obj}})
        return result['created']['managed']['id']


def _openssl(arguments, data=None):
    try:
        result = subprocess.run(['openssl', *arguments], input=data, capture_output=True, timeout=30, check=True)
        return result.stdout.decode()
    except (OSError, subprocess.SubprocessError, UnicodeError):
        raise ReconciliationError('OpenSSL mail key operation failed') from None


def _password_hash(email, password):
    # Stable per-account salt yields the same native credential on repeated apply.
    salt = hashlib.sha256(('noebs.mail/v1:' + email).encode()).hexdigest()[:16]
    return _openssl(['passwd', '-6', '-salt', salt, '-stdin'], (password + '\n').encode()).strip()


def _expression(value):
    return {'match': {}, 'else': value}


def _configure_certificate(client, config, secrets):
    # v0.16.21 requires a nonempty publication set for DNS-01. Keep its
    # certificate-only domain restricted to DKIM, whose record set is empty
    # because it has no mailboxes or signing keys. This enables challenges
    # without publishing CAA restrictions that block Caddy's separate account.
    existing_hosts = [d['id'] for d in client.objects('Domain') if d.get('name') == config['hostname']]
    if existing_hosts and any(key.get('domainId') in existing_hosts for key in client.objects('DkimSignature')):
        raise ReconciliationError('The certificate-only domain must not own DKIM signing keys')
    dns_id = client.upsert('DnsServer', {'description': 'NoEBS ACME challenges'}, {
        '@type': 'Cloudflare', 'description': 'NoEBS ACME challenges',
        'secret': {'@type': 'Value', 'secret': secrets['cloudflare_api_token']}})
    acme_id = client.upsert('AcmeProvider', {'directory': 'https://acme-v02.api.letsencrypt.org/directory'}, {
        'directory': 'https://acme-v02.api.letsencrypt.org/directory', 'challengeType': 'Dns01',
        'contact': {'mailto:postmaster@' + config['domains'][0]: True}})
    client.upsert('Domain', {'name': config['hostname']}, {
        'name': config['hostname'], 'description': 'NoEBS mail service certificate',
        'isEnabled': True, 'allowRelaying': False,
        'dkimManagement': {'@type': 'Manual'},
        'dnsManagement': {'@type': 'Automatic', 'dnsServerId': dns_id,
                          'origin': max((d for d in config['domains'] if config['hostname'].endswith('.' + d)), key=len),
                          'publishRecords': {'dkim': True}},
        'certificateManagement': {'@type': 'Automatic', 'acmeProviderId': acme_id,
                                  'subjectAlternativeNames': {}}})


def _configure_logging(client):
    client.upsert('Tracer', {'@type': 'Stdout'}, {
        '@type': 'Stdout', 'enable': True, 'level': 'info', 'ansi': False,
        'multiline': False, 'buffered': True})
    # The native bare-metal default cannot write outside this container's
    # persistent mounts. Docker owns bounded log storage and rotation.
    for tracer in client.objects('Tracer'):
        if (tracer.get('@type') == 'Log' and tracer.get('path') == '/var/log/stalwart'
                and tracer.get('prefix') == 'stalwart.log' and tracer.get('enable')):
            client.call('Tracer', 'set', {'update': {tracer['id']: {'enable': False}}})


def _configure_mail(client, config, secrets):
    _configure_logging(client)
    domain_ids = {}
    for name in config['domains']:
        domain_ids[name] = client.upsert('Domain', {'name': name}, {
            'name': name, 'description': 'NoEBS managed mail domain', 'isEnabled': True,
            'allowRelaying': False, 'catchAllAddress': None,
            'dnsManagement': {'@type': 'Manual'}, 'dkimManagement': {'@type': 'Manual'},
            'certificateManagement': {'@type': 'Manual'}})
    client.singleton('SystemSettings', {
        'defaultHostname': config['hostname'], 'defaultDomainId': domain_ids[config['domains'][0]],
        'mailExchangers': {'0': {'hostname': config['hostname'], 'priority': 10}},
        'services': {p: {'hostname': config['hostname'], 'cleartext': False} for p in ('smtp', 'imap', 'jmap')},
        'proxyTrustedNetworks': {}})
    for kind in ('BlobStore', 'SearchStore', 'InMemoryStore'):
        client.singleton(kind, {'@type': 'Default'})
    # HTTP is reachable only through the loopback-published trusted Caddy route.
    # That route discards Forwarded and overwrites X-Forwarded-For.
    client.singleton('Http', {'useXForwarded': True})
    client.singleton('MtaStageAuth', {
        'saslMechanisms': {'match': {'0': {'if': 'local_port != 25 && is_tls', 'then': '[plain, login]'}}, 'else': 'false'},
        'require': _expression('local_port != 25'), 'mustMatchSender': _expression('true')})
    client.singleton('MtaStageRcpt', {'allowRelaying': _expression('!is_empty(authenticated_as)')})
    for name, port, protocol, implicit in [('smtp', 25, 'smtp', False), ('submissions', 465, 'smtp', True),
                                           ('submission', 587, 'smtp', False), ('imaps', 993, 'imap', True),
                                           ('http', 8080, 'http', False)]:
        client.upsert('NetworkListener', {'name': name}, {
            'name': name, 'bind': {'0.0.0.0:' + str(port): True}, 'protocol': protocol,
            'useTls': protocol != 'http', 'tlsImplicit': implicit, 'maxConnections': 256})
    client.upsert('MtaRoute', {'name': 'mx'}, {'@type': 'Mx', 'name': 'mx', 'ipLookupStrategy': 'v4Only'})
    client.upsert('MtaRoute', {'name': 'local'}, {'@type': 'Local', 'name': 'local'})
    # Docker supplies the public egress address through NAT. Binding the host-only
    # public address inside the isolated bridge namespace would fail.
    client.upsert('MtaConnectionStrategy', {'name': 'default'}, {
        'name': 'default', 'ehloHostname': config['hostname'], 'sourceIps': {}})
    for box in config['mailboxes']:
        local, domain = box['email'].split('@')
        desired = {
            '@type': 'User', 'name': local, 'domainId': domain_ids[domain], 'description': box['name'],
            'roles': {'@type': box['role']}, 'permissions': {'@type': 'Inherit'},
            'quotas': {'maxDiskQuota': box['quota_bytes']}, 'aliases': {}}
        def credentials():
            return {'credentials': {'0': {'@type': 'Password', 'secret': _password_hash(
                box['email'], secrets['mailbox_passwords'][box['email']])}}}
        if box['password_policy'] == 'Managed':
            desired.update(credentials())
        client.upsert('Account', {'name': local, 'domainId': domain_ids[domain]}, desired,
                      create_only=credentials if box['password_policy'] == 'Initial' else None)
    records = []
    for domain, domain_id in domain_ids.items():
        identity = {'domainId': domain_id, 'selector': config['dkim_selector']}
        key_id = client.upsert('DkimSignature', identity, {
            '@type': 'Dkim1RsaSha256', **identity, 'stage': 'active'}, create_only=lambda: {
                'privateKey': {'@type': 'Text', 'secret': _openssl(['genrsa', '-traditional', '2048'])}})
        key = client.call('DkimSignature', 'get', {'ids': [key_id], 'properties': ['publicKey']})['list'][0]
        records.append({'domain': domain, 'selector': config['dkim_selector'],
                        'name': config['dkim_selector'] + '._domainkey.' + domain, 'type': 'TXT',
                        'content': 'v=DKIM1; k=rsa; h=sha256; p=' + key['publicKey']})
    return records


def reconcile(config, secrets, base_url='http://127.0.0.1:18080'):
    validate(config, secrets)
    if not shutil.which('openssl'):
        raise ConfigurationError('Host OpenSSL is required before applying mail configuration')
    client = NativeClient(base_url, secrets['admin_password'])
    deadline = time.monotonic() + 60
    while True:
        try:
            client.objects('Domain')
            break
        except ReconciliationError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)
    records = _configure_mail(client, config, secrets)
    _configure_certificate(client, config, secrets)
    return {'domains': config['domains'], 'mailboxes': [m['email'] for m in config['mailboxes']],
            'dkim_records': records, 'dns_publication': 'external', 'restart_required': True}
