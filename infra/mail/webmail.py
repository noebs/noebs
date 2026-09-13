"""Render the official Roundcube client for the existing Stalwart accounts."""
from pathlib import Path
import re

import yaml

from runtime import HOSTNAME, InvalidMailConfiguration


class InvalidWebmailConfiguration(InvalidMailConfiguration):
    """The optional webmail deployment is incomplete or invalid."""


def validate(config, secrets=None):
    webmail = config.get('webmail') if isinstance(config, dict) else None
    if not isinstance(webmail, dict) or set(webmail) != {'hostname', 'image', 'loopback_port', 'resources'}:
        raise InvalidWebmailConfiguration('Webmail requires explicit hostname, image, loopback_port and resources')
    hostname = webmail['hostname']
    domains = config.get('domains')
    if (not isinstance(hostname, str) or not HOSTNAME.fullmatch(hostname)
            or hostname == config.get('hostname') or not isinstance(domains, list)
            or any(not isinstance(domain, str) or not HOSTNAME.fullmatch(domain) for domain in domains)
            or len([domain for domain in domains if hostname.endswith('.' + domain)]) != 1):
        raise InvalidWebmailConfiguration('Webmail requires a distinct hostname under one managed mail domain')
    if type(webmail['loopback_port']) is not int or not 1024 <= webmail['loopback_port'] <= 65535 or webmail['loopback_port'] == 18080:
        raise InvalidWebmailConfiguration('Webmail requires an explicit unprivileged loopback port distinct from Stalwart')
    if not isinstance(webmail['image'], str) or not re.fullmatch(
            r'roundcube/roundcubemail:[0-9]+\.[0-9]+\.[0-9]+-apache-nonroot@sha256:[0-9a-f]{64}', webmail['image']):
        raise InvalidWebmailConfiguration('Webmail requires the official versioned nonroot Apache image pinned by SHA256')
    resources = webmail['resources']
    if (not isinstance(resources, dict) or set(resources) != {'cpus', 'memory_mib', 'pids'}
            or type(resources['cpus']) not in (int, float) or not 0 < resources['cpus'] <= 4
            or type(resources['memory_mib']) is not int or not 256 <= resources['memory_mib'] <= 4096
            or type(resources['pids']) is not int or not 32 <= resources['pids'] <= 1024):
        raise InvalidWebmailConfiguration('Webmail requires bounded explicit CPU, memory and process limits')
    if secrets is not None:
        key = secrets.get('webmail_des_key') if isinstance(secrets, dict) else None
        if not isinstance(key, str) or len(key) != 24 or any(ord(character) < 33 or ord(character) > 126 for character in key):
            raise InvalidWebmailConfiguration('Webmail requires a persistent 24-character printable ASCII encryption key')
        if key in [secrets.get('admin_password'), secrets.get('cloudflare_api_token'),
                   *secrets.get('mailbox_passwords', {}).values()]:
            raise InvalidWebmailConfiguration('The webmail encryption key must be separate from mail credentials')
    return webmail


def render_service(config):
    """Render validated settings; mailbox passwords never enter this manifest."""
    webmail = config['webmail']
    values = {
        'WEBMAIL_IMAGE': webmail['image'], 'WEBMAIL_PORT': str(webmail['loopback_port']),
        'WEBMAIL_STATE': str(Path(config['state_directory']) / 'webmail'),
        'MAIL_HOSTNAME': config['hostname'], 'WEBMAIL_HOSTNAME': webmail['hostname'],
        'WEBMAIL_CPUS': str(webmail['resources']['cpus']),
        'WEBMAIL_MEMORY_MIB': str(webmail['resources']['memory_mib']),
        'WEBMAIL_PIDS': str(webmail['resources']['pids']),
    }
    template = Path(__file__).with_name('webmail-compose.yaml').read_text()
    content = re.sub(r'\$\{([A-Z_][A-Z_0-9]*):\?[^}]+\}', lambda match: values[match[1]], template)
    if '${' in content:
        raise InvalidWebmailConfiguration('Webmail Compose contains an unresolved setting')
    return yaml.safe_load(content)


def php_string(value):
    return "'" + value.replace('\\', '\\\\').replace("'", "\\'") + "'"


def render_php(config):
    """Native client policy only: Stalwart remains the mailbox/password authority."""
    hostname = config['webmail']['hostname']
    mailhost = config['hostname']
    return """<?php
// Managed by the NoEBS mail deployment. Credentials are supplied by each user.
$config['product_name'] = 'Webmail';
$config['enable_installer'] = false;
$config['use_https'] = true;
$config['force_https'] = false;
$config['trusted_host_patterns'] = [%s];
$config['request_path'] = %s;
$config['session_samesite'] = 'Lax';
$config['session_storage'] = 'db';
$config['session_lifetime'] = 30;
$config['login_username_filter'] = 'email';
$config['username_domain'] = '';
$config['login_autocomplete'] = 2;
$config['imap_host'] = %s;
$config['smtp_host'] = %s;
$config['smtp_user'] = '%%u';
$config['smtp_pass'] = '%%p';
$config['imap_conn_options'] = ['ssl' => ['verify_peer' => true, 'verify_peer_name' => true, 'allow_self_signed' => false]];
$config['smtp_conn_options'] = $config['imap_conn_options'];
$config['identities_level'] = 3;
$config['plugins'] = ['archive', 'zipdownload'];
$config['max_message_size'] = '20M';
$config['smtp_log'] = false;
$config['imap_debug'] = false;
$config['smtp_debug'] = false;
$config['session_debug'] = false;
""" % (php_string('^' + re.escape(hostname) + '$'), php_string('https://' + hostname + '/'),
       php_string('ssl://' + mailhost + ':993'), php_string('ssl://' + mailhost + ':465'))


def render_php_ini():
    return ('session.cookie_secure=1\nsession.cookie_httponly=1\nsession.cookie_samesite=Lax\n'
            'memory_limit=128M\nupload_max_filesize=10M\npost_max_size=12M\nexpose_php=Off\n')
