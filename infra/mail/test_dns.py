import contextlib
import copy
import io
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import yaml

import dns
from dns import InvalidMailDNS, desired_records, dkim_from_receipt, plan_records


# Public half of a disposable 2048-bit RSA test key; no private key is retained.
DKIM_KEY = 'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA2CsZvFDmd0MoMxbe2sD6aKSz+LXGjpooc2g55+1IBP8Z7BTZSVNt9DbLVRTmEWVUm1W6phh87AHaUeIMU3ZChG8M1xOGN+5rexjPwbwmQ//kLC76Sh3HhrpdNiiIQDPMYiJ2olKG91UhtwghQDNtNs0zgTFGJmzE2QjwaL3GPDBl0MJU3n/vr/wXhawj5mQTS6tBMPV0mqsptZbFnQRVfS7CJu5Ji3zvkyHF8tBh4i8HPZCHtWJUhceqr2Z+jU5lsOFo3jSiFmfZEpbfoP2tXKeOgWSXRRr+y+4jsRyihvcBbziJYeNJ1/zW7YWs/lSfVN8X4d6JNHzTs0g//sk3RwIDAQAB'
DKIM_CONTENT = 'v=DKIM1; k=rsa; h=sha256; p=' + DKIM_KEY


def receipt(config):
    return {
        'api_version': 'noebs.mail.receipt/v1',
        **{field: config[field] for field in ['ssh_destination', 'hostname', 'image']},
        'native': {'domains': config['domains'], 'dkim_records': [
            {'domain': domain, 'selector': config['dkim_selector'], 'type': 'TXT',
             'name': config['dkim_selector'] + '._domainkey.' + domain, 'content': DKIM_CONTENT}
            for domain in config['domains']]},
    }


class DNSTests(unittest.TestCase):
    def setUp(self):
        self.spf = {'type': 'TXT', 'name': 'example.com', 'content': 'v=spf1 ip4:213.199.63.78 -all', 'ttl': 300}

    def test_preserves_web_and_verification_records(self):
        existing = [
            {'id': 'web', 'type': 'A', 'name': 'example.com', 'content': '192.0.2.1'},
            {'id': 'verify', 'type': 'TXT', 'name': 'example.com', 'content': 'site-verification=keep'},
            {'id': 'spf', 'type': 'TXT', 'name': 'example.com', 'content': 'v=spf1 mx ~all'},
        ]
        plan = plan_records(existing, [self.spf], "example.com")
        self.assertEqual([(action['method'], action['id']) for action in plan], [('PUT', 'spf')])

    def test_replaces_old_mx_and_removes_only_duplicates(self):
        wanted = {'type': 'MX', 'name': 'example.com', 'content': 'mail.example.com', 'priority': 10, 'ttl': 300}
        existing = [dict(wanted, id='current'), dict(wanted, id='old', content='old.example.net')]
        self.assertEqual(plan_records(existing, [wanted], "example.com"), [{'method': 'DELETE', 'id': 'old', 'record': {'type': 'MX', 'name': 'example.com', 'content': 'old.example.net'}}])

    def test_repeat_apply_has_no_changes(self):
        self.assertEqual(plan_records([dict(self.spf, id='spf')], [self.spf], "example.com"), [])

    def test_cname_conflict_fails_before_mutation(self):
        with self.assertRaisesRegex(InvalidMailDNS, 'CNAME'):
            plan_records([{'id': 'alias', 'name': 'example.com', 'type': 'CNAME'}], [self.spf], "other.com")

    def test_cloudflare_apex_alias_is_preserved_with_mail_records(self):
        existing = [{'id': 'website', 'type': 'CNAME', 'name': 'example.com', 'content': 'site.pages.dev', 'proxied': True}]
        mx = {'type': 'MX', 'name': 'example.com', 'content': 'mail.example.com', 'priority': 10, 'ttl': 300}
        actions = plan_records(existing, [self.spf, mx], 'example.com')
        self.assertEqual([action['method'] for action in actions], ['POST', 'POST'])
        self.assertTrue(all(action['record']['type'] in ('TXT', 'MX') for action in actions))

    def test_apex_flattening_does_not_allow_replacing_the_website_alias(self):
        existing = [{'id': 'website', 'type': 'CNAME', 'name': 'example.com', 'content': 'site.pages.dev'}]
        with self.assertRaisesRegex(InvalidMailDNS, 'CNAME'):
            plan_records(existing, [{'type': 'A', 'name': 'example.com', 'content': '213.199.63.78'}], 'example.com')

    def test_full_plan_requires_actual_dkim(self):
        with self.assertRaisesRegex(InvalidMailDNS, 'DKIM'):
            desired_records('mail.example.com', '213.199.63.78', ['example.com'], 300)

    def test_hostname_bootstrap_does_not_change_domain_routing(self):
        records = desired_records('mail.example.com', '213.199.63.78', ['example.com', 'example.net'], 300, hostname_only=True)
        self.assertEqual(list(records), ['example.com'])
        self.assertEqual(records['example.com'], [{'type': 'A', 'name': 'mail.example.com', 'content': '213.199.63.78', 'ttl': 300, 'proxied': False}])

    def test_full_plan_never_owns_website_addresses(self):
        dkim = {'example.com': [{'selector': 's1', 'content': DKIM_CONTENT}]}
        records = desired_records('mail.example.com', '213.199.63.78', ['example.com'], 300, dkim)['example.com']
        self.assertEqual([r['name'] for r in records if r['type'] == 'A'], ['mail.example.com'])
        self.assertEqual({r['type'] for r in records}, {'A', 'MX', 'TXT'})

    def test_webmail_hostname_bootstrap_publishes_only_explicit_hosts(self):
        records = desired_records('mail.example.com', '213.199.63.78', ['example.com', 'example.net'],
                                  300, hostname_only=True, webmail_hostname='webmail.example.net')
        self.assertEqual(set(records), {'example.com', 'example.net'})
        self.assertEqual(records['example.net'], [{'type': 'A', 'name': 'webmail.example.net',
                                                'content': '213.199.63.78', 'ttl': 300, 'proxied': False}])
        self.assertTrue(all(record['type'] == 'A' for values in records.values() for record in values))
        with self.assertRaises(InvalidMailDNS):
            desired_records('mail.example.com', '213.199.63.78', ['example.com'], 300,
                            hostname_only=True, webmail_hostname='other.test')

    def test_webmail_publication_preserves_websites_and_clears_only_its_owned_ipv6(self):
        domains = ['example.com', 'example.net']
        dkim = {domain: [{'selector': 's1', 'content': DKIM_CONTENT}] for domain in domains}
        records = desired_records('mail.example.com', '213.199.63.78', domains, 300, dkim,
                                  webmail_hostname='webmail.example.net')['example.net']
        desired_a = next(record for record in records if record['type'] == 'A')
        existing = [dict(record, id='managed-' + str(index)) for index, record in enumerate(records)] + [
            {'id': 'site', 'type': 'A', 'name': 'example.net', 'content': '192.0.2.1'},
            {'id': 'site-ipv6', 'type': 'AAAA', 'name': 'example.net', 'content': '2001:db8::1'},
            {'id': 'webmail-ipv6', 'type': 'AAAA', 'name': 'webmail.example.net', 'content': '2001:db8::2'},
        ]
        self.assertEqual(desired_a['name'], 'webmail.example.net')
        self.assertEqual([action['id'] for action in plan_records(existing, records, "example.com")], ['webmail-ipv6'])

    def test_selector_txt_publication_preserves_unrelated_txt_and_old_selectors(self):
        wanted = {'type': 'TXT', 'name': 's1._domainkey.example.com', 'content': DKIM_CONTENT, 'ttl': 300}
        existing = [dict(wanted, id='current'),
                    {'id': 'verify', 'type': 'TXT', 'name': wanted['name'], 'content': 'site-verification=keep'},
                    dict(wanted, id='old-selector', name='s0._domainkey.example.com')]
        self.assertEqual(plan_records(existing, [wanted], "example.com"), [])

    def test_ipv4_mail_routing_removes_only_mail_host_aaaa(self):
        wanted = {'type': 'A', 'name': 'mail.example.com', 'content': '213.199.63.78', 'ttl': 300, 'proxied': False}
        existing = [dict(wanted, id='a'),
                    {'id': 'mail-ipv6', 'type': 'AAAA', 'name': wanted['name'], 'content': '2001:db8::1'},
                    {'id': 'web-ipv6', 'type': 'AAAA', 'name': 'example.com', 'content': '2001:db8::2'}]
        self.assertEqual([action['id'] for action in plan_records(existing, [wanted], "example.com")], ['mail-ipv6'])

    def test_quote_chunks_do_not_create_repeat_updates(self):
        self.assertEqual(plan_records([dict(self.spf, id='spf', content='"v=spf1 " "ip4:213.199.63.78 -all"')], [self.spf], "example.com"), [])

    def test_malformed_or_duplicate_signers_fail_before_planning(self):
        for entries in [[{'selector': 's1', 'content': DKIM_CONTENT}] * 2,
                        [{'selector': 'bad.selector', 'content': DKIM_CONTENT}],
                        [{'selector': 's1', 'content': 'v=DKIM1; k=rsa; p=bad'}],
                        [{'selector': 's1', 'content': DKIM_CONTENT + '; p=' + DKIM_KEY}]]:
            with self.subTest(entries=entries), self.assertRaises(InvalidMailDNS):
                desired_records('mail.example.com', '213.199.63.78', ['example.com'], 300, {'example.com': entries})


class ReceiptBoundaryTests(unittest.TestCase):
    def setUp(self):
        from test_runtime import configuration
        self.config = configuration()

    def test_native_receipt_converts_exact_current_signers_without_mutation(self):
        value = receipt(self.config)
        before = copy.deepcopy(value)
        expected = {domain: [{'selector': self.config['dkim_selector'], 'content': DKIM_CONTENT}]
                    for domain in self.config['domains']}
        self.assertEqual(dkim_from_receipt(value, self.config), expected)
        self.assertEqual(value, before)

    def test_stale_hosts_images_domains_or_selector_are_rejected(self):
        cases = []
        for field, value in [('api_version', ''), ('ssh_destination', 'other@host'),
                             ('hostname', 'other.example.com'), ('image', 'stalwart:latest')]:
            candidate = receipt(self.config)
            candidate[field] = value
            cases.append(candidate)
        candidate = receipt(self.config)
        candidate['native']['domains'] = ['outside.example.com']
        cases.append(candidate)
        for field, value in [('domain', 'outside.example.com'), ('selector', 'old'), ('type', 'A'),
                             ('name', 'www.noebs.sd'), ('content', 'private-value')]:
            candidate = receipt(self.config)
            candidate['native']['dkim_records'][0][field] = value
            cases.append(candidate)
        duplicate = receipt(self.config)
        duplicate['native']['dkim_records'][1] = duplicate['native']['dkim_records'][0]
        cases.append(duplicate)
        for value in cases:
            with self.subTest(value=value), self.assertRaises(InvalidMailDNS):
                dkim_from_receipt(value, self.config)

    def test_invalid_receipt_stops_before_decryption_or_cloudflare(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = root / 'deployment.yaml'
            config.write_text(yaml.safe_dump(self.config))
            invalid = root / 'receipt.json'
            invalid.write_text('{}')
            argv = ['dns.py', '--config', str(config), '--secrets', 'test.secrets.yaml', '--receipt', str(invalid)]
            with patch.object(sys, 'argv', argv), patch.object(dns.subprocess, 'run') as decrypt, \
                 patch.object(dns, 'Cloudflare') as client:
                with self.assertRaises(InvalidMailDNS):
                    dns.main()
                decrypt.assert_not_called()
                client.assert_not_called()

    def test_malformed_decrypted_credentials_do_not_escape_in_error_or_logs(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'deployment.yaml'
            config.write_text(yaml.safe_dump(self.config))
            argv = ['dns.py', '--config', str(config), '--secrets', 'test.secrets.yaml', '--hostname-only']
            result = SimpleNamespace(returncode=0, stdout=b'{"cloudflare_api_token":"private-value",broken')
            with patch.object(sys, 'argv', argv), patch.object(dns.subprocess, 'run', return_value=result), \
                 patch.object(dns, 'Cloudflare') as client, contextlib.redirect_stdout(io.StringIO()) as output:
                with self.assertRaises(InvalidMailDNS) as failure:
                    dns.main()
                client.assert_not_called()
                self.assertNotIn('private-value', str(failure.exception))
                self.assertNotIn('private-value', output.getvalue())

    def test_full_receipt_cli_plans_dns_without_manual_conversion(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = root / 'deployment.yaml'
            config.write_text(yaml.safe_dump(self.config))
            applied = root / 'receipt.json'
            applied.write_text(json.dumps(receipt(self.config)))
            argv = ['dns.py', '--config', str(config), '--secrets', 'test.secrets.yaml', '--receipt', str(applied)]
            result = SimpleNamespace(returncode=0, stdout=b'{"cloudflare_api_token":"private-value"}')
            with patch.object(sys, 'argv', argv), patch.object(dns.subprocess, 'run', return_value=result), \
                 patch.object(dns, 'Cloudflare') as client, contextlib.redirect_stdout(io.StringIO()) as output:
                client.return_value.zone.side_effect = self.config['domains']
                client.return_value.records.return_value = []
                self.assertEqual(dns.main(), 0)
                client.return_value.request.assert_not_called()
                planned = json.loads(output.getvalue())
                self.assertEqual(set(planned['changes']), set(self.config['domains']))
                hosts = [action['record']['name'] for actions in planned['changes'].values()
                         for action in actions if action['record']['type'] == 'A']
                self.assertEqual(set(hosts), {self.config['hostname'], self.config['webmail']['hostname']})
                self.assertNotIn('private-value', output.getvalue())

    def test_hostname_only_cli_includes_configured_webmail_without_mail_cutover(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'deployment.yaml'
            config.write_text(yaml.safe_dump(self.config))
            argv = ['dns.py', '--config', str(config), '--secrets', 'test.secrets.yaml', '--hostname-only']
            result = SimpleNamespace(returncode=0, stdout=b'{"cloudflare_api_token":"private-value"}')
            with patch.object(sys, 'argv', argv), patch.object(dns.subprocess, 'run', return_value=result), \
                 patch.object(dns, 'Cloudflare') as client, contextlib.redirect_stdout(io.StringIO()) as output:
                client.return_value.zone.side_effect = ['noebs.sd', 'adonese.sd']
                client.return_value.records.return_value = []
                self.assertEqual(dns.main(), 0)
                planned = json.loads(output.getvalue())
                self.assertEqual(set(planned['changes']), {'noebs.sd', 'adonese.sd'})
                self.assertEqual([action['record']['type'] for actions in planned['changes'].values()
                                  for action in actions], ['A', 'A'])
                client.return_value.request.assert_not_called()


if __name__ == '__main__':
    unittest.main()
