import contextlib
import io
import sys
import unittest
from unittest.mock import Mock, patch

import verify
from verify import MailReadinessError, check_dkim, check_domain_dns, check_host_dns, check_relay_denied
from test_dns import DKIM_CONTENT, DKIM_KEY


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        self.hostname = 'mail.example.com'
        self.ipv4 = '213.199.63.78'
        self.records = {
            (self.hostname, 'A'): [self.ipv4],
            ('78.63.199.213.in-addr.arpa', 'PTR'): [self.hostname + '.'],
            ('example.com', 'MX'): ['10 ' + self.hostname + '.'],
            ('example.com', 'TXT'): ['unrelated-verification=value', 'v=spf1 mx -all'],
            ('_dmarc.example.com', 'TXT'): ['v=DMARC1; p=none'],
            ('mail._domainkey.example.com', 'TXT'): [DKIM_CONTENT],
        }
        self.query = lambda name, kind: self.records.get((name, kind), [])

    def test_consistent_dns_passes(self):
        check_host_dns(self.hostname, self.ipv4, self.query)
        check_domain_dns('example.com', self.hostname, self.ipv4, self.query)
        check_dkim('example.com', 'mail', self.query)

    def test_wrong_ptr_fails(self):
        self.records[('78.63.199.213.in-addr.arpa', 'PTR')] = ['provider.example.net.']
        with self.assertRaisesRegex(MailReadinessError, 'PTR'):
            check_host_dns(self.hostname, self.ipv4, self.query)

    def test_stale_ipv6_cannot_be_reported_ready_for_ipv4_runtime(self):
        self.records[(self.hostname, 'AAAA')] = ['2001:db8::1']
        with self.assertRaisesRegex(MailReadinessError, 'IPv6'):
            check_host_dns(self.hostname, self.ipv4, self.query)

    def test_existing_mx_is_not_reported_ready(self):
        self.records[('example.com', 'MX')] = ['10 old.example.net.']
        with self.assertRaisesRegex(MailReadinessError, 'MX'):
            check_domain_dns('example.com', self.hostname, self.ipv4, self.query)

    def test_duplicate_spf_fails(self):
        self.records[('example.com', 'TXT')].append('v=spf1 -all')
        with self.assertRaisesRegex(MailReadinessError, 'exactly one SPF'):
            check_domain_dns('example.com', self.hostname, self.ipv4, self.query)

    def test_unrestricted_spf_fails(self):
        self.records[('example.com', 'TXT')] = ['v=spf1 mx +all']
        with self.assertRaisesRegex(MailReadinessError, 'restrictive'):
            check_domain_dns('example.com', self.hostname, self.ipv4, self.query)

    def test_explicit_positive_ipv4_spf_qualifier_passes(self):
        self.records[('example.com', 'TXT')] = ['v=spf1 +ip4:' + self.ipv4 + ' -all']
        check_domain_dns('example.com', self.hostname, self.ipv4, self.query)

    def test_revoked_dkim_fails(self):
        self.records[('mail._domainkey.example.com', 'TXT')] = ['v=DKIM1; p=']
        with self.assertRaisesRegex(MailReadinessError, 'nonempty'):
            check_dkim('example.com', 'mail', self.query)

    def test_duplicate_dkim_tags_or_short_keys_fail(self):
        for value in [DKIM_CONTENT + '; p=' + DKIM_KEY, 'v=DKIM1; k=rsa; p=c2hvcnQ=']:
            with self.subTest(value=value):
                self.records[('mail._domainkey.example.com', 'TXT')] = [value]
                with self.assertRaises(MailReadinessError):
                    check_dkim('example.com', 'mail', self.query)

    def test_unrelated_selector_txt_is_preserved_and_ignored(self):
        self.records[('mail._domainkey.example.com', 'TXT')].append('site-verification=keep')
        check_dkim('example.com', 'mail', self.query)

    def test_contradictory_spf_and_duplicate_dmarc_policies_fail(self):
        for value in ['v=spf1 -all mx -all', 'v=spf1 -mx mx -all']:
            with self.subTest(value=value):
                self.records[('example.com', 'TXT')] = [value]
                with self.assertRaisesRegex(MailReadinessError, 'contradictory'):
                    check_domain_dns('example.com', self.hostname, self.ipv4, self.query)
        self.records[('example.com', 'TXT')] = ['v=spf1 mx -all']
        self.records[('_dmarc.example.com', 'TXT')] = ['v=DMARC1; p=none; p=reject']
        with self.assertRaisesRegex(MailReadinessError, 'distinct'):
            check_domain_dns('example.com', self.hostname, self.ipv4, self.query)

    def test_relay_rejection_sends_no_data(self):
        client = Mock()
        client.mail.return_value = (250, b'OK')
        client.rcpt.return_value = (550, b'5.1.2 Relay not allowed.')
        check_relay_denied(client)
        client.data.assert_not_called()
        client.rset.assert_called_once()

    def test_recipient_errors_are_not_proof_of_relay_rejection(self):
        for code, message in [(550, b'5.1.1 Mailbox does not exist.'),
                              (550, b'5.1.2 Domain does not exist.'),
                              (553, b'Invalid address'), (554, b'Policy error')]:
            with self.subTest(code=code, message=message):
                client = Mock()
                client.mail.return_value = (250, b'OK')
                client.rcpt.return_value = (code, message)
                with self.assertRaisesRegex(MailReadinessError, 'relay policy'):
                    check_relay_denied(client)
                client.data.assert_not_called()
    def test_accepted_external_recipient_fails_without_sending(self):
        client = Mock()
        client.mail.return_value = (250, b'OK')
        client.rcpt.return_value = (250, b'Accepted')
        with self.assertRaisesRegex(MailReadinessError, 'not rejected'):
            check_relay_denied(client)
        client.data.assert_not_called()

    def test_transient_error_is_not_proof_of_relay_rejection(self):
        client = Mock()
        client.mail.return_value = (250, b'OK')
        client.rcpt.return_value = (451, b'Try later')
        with self.assertRaises(MailReadinessError):
            check_relay_denied(client)
        client.data.assert_not_called()

    def test_pinned_stalwart_auth_required_503_passes_without_a_recipient(self):
        client = Mock()
        client.mail.return_value = (503, b'5.5.1 You must authenticate first.')
        check_relay_denied(client)
        client.rcpt.assert_not_called()
        client.data.assert_not_called()

    def test_other_503_errors_do_not_prove_authentication_is_required(self):
        for message in [b'5.5.1 Bad sequence of commands', b'generic error', b'']:
            with self.subTest(message=message):
                client = Mock()
                client.mail.return_value = (503, message)
                with self.assertRaises(MailReadinessError):
                    check_relay_denied(client)
                client.data.assert_not_called()

    def test_missing_selector_for_a_domain_stops_before_dns_or_smtp(self):
        args = ['verify.py', '--hostname', self.hostname, '--ipv4', self.ipv4,
                '--domain', 'example.com', '--domain', 'example.org', '--dkim', 'example.com:mail']
        with patch.object(sys, 'argv', args), patch.object(verify, 'check_host_dns') as query, \
             contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                verify.main()
            query.assert_not_called()


if __name__ == '__main__':
    unittest.main()
