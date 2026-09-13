import copy
import json
import os
import unittest
from unittest.mock import patch

from runtime import render_compose, validate_runtime
from test_runtime import configuration
import webmail


TEST_KEY = '0123456789abcdef01234567'


class WebmailConfigurationTest(unittest.TestCase):
    def test_incomplete_webmail_fails_without_defaulting_or_mutating(self):
        for value in [None, {}, {'hostname': 'webmail.adonese.sd'}]:
            config = configuration()
            config['webmail'] = value
            before = copy.deepcopy(config)
            with self.subTest(value=value), self.assertRaises(webmail.InvalidWebmailConfiguration):
                validate_runtime(config)
            self.assertEqual(config, before)

    def test_unsafe_host_image_ports_and_resources_fail_at_boundary(self):
        for field, value in [('hostname', 'mail.noebs.sd'), ('hostname', 'foreign.example.com'),
                             ('hostname', "a';php();.adonese.sd"), ('loopback_port', True),
                             ('loopback_port', 18080), ('loopback_port', 80), ('loopback_port', 65536),
                             ('image', 'roundcube/roundcubemail:latest'),
                             ('image', 'untrusted/roundcube:1.7.4@sha256:' + 'a' * 64),
                             ('resources', {}), ('resources', {'cpus': True, 'memory_mib': 512, 'pids': 128})]:
            config = configuration()
            config['webmail'][field] = value
            with self.subTest(field=field, value=value), self.assertRaises(webmail.InvalidWebmailConfiguration):
                webmail.validate(config)

    def test_stable_key_is_required_and_errors_never_repeat_its_value(self):
        for value in [None, '', 'private-short-value', 'a' * 25, 'a' * 23 + '\n']:
            with self.subTest(value=value), self.assertRaises(webmail.InvalidWebmailConfiguration) as error:
                webmail.validate(configuration(), {'webmail_des_key': value})
            if value:
                self.assertNotIn(value, str(error.exception))
        webmail.validate(configuration(), {'webmail_des_key': TEST_KEY})
        with self.assertRaisesRegex(webmail.InvalidWebmailConfiguration, 'separate'):
            webmail.validate(configuration(), {'webmail_des_key': TEST_KEY, 'admin_password': TEST_KEY})

    def test_client_is_nonroot_private_and_stores_only_metadata(self):
        service = render_compose(validate_runtime(configuration()))['services']['webmail']
        self.assertEqual(service['ports'], ['127.0.0.1:18089:8000'])
        self.assertEqual(service['user'], '33:33')
        self.assertTrue(service['read_only'])
        self.assertEqual(service['cap_drop'], ['ALL'])
        self.assertNotIn('network_mode', service)
        self.assertEqual(service['environment']['ROUNDCUBEMAIL_DB_TYPE'], 'sqlite')
        self.assertEqual(service['environment']['ROUNDCUBEMAIL_DEFAULT_HOST'], 'ssl://mail.noebs.sd')
        self.assertEqual(service['environment']['ROUNDCUBEMAIL_SMTP_PORT'], '465')
        self.assertEqual(service['environment']['ROUNDCUBEMAIL_USERNAME_DOMAIN'], '')
        self.assertEqual(len(service['volumes']), 4)
        self.assertFalse(any('/var/www/html' in value for value in service['volumes']))
        self.assertFalse(any('PASSWORD' in key or 'DES_KEY' in key for key in service['environment']))
        self.assertIn('/var/lib/noebs-mail/webmail/db:/var/roundcube/db', service['volumes'])
        self.assertNotIn('webmail', render_compose(configuration(), recovery=True)['services'])

    def test_native_policy_enforces_tls_account_identity_and_secure_browser_sessions(self):
        policy = webmail.render_php(configuration())
        for value in ["$config['smtp_user'] = '%u'", "$config['smtp_pass'] = '%p'",
                      "'ssl://mail.noebs.sd:993'", "'ssl://mail.noebs.sd:465'",
                      "'verify_peer' => true", "'verify_peer_name' => true", "'allow_self_signed' => false",
                      "$config['use_https'] = true", "$config['identities_level'] = 3",
                      "$config['login_username_filter'] = 'email'", "$config['enable_installer'] = false"]:
            self.assertIn(value, policy)
        self.assertNotIn(TEST_KEY, policy)
        self.assertNotIn('postmaster@', policy)
        self.assertIn('session.cookie_secure=1', webmail.render_php_ini())
        self.assertIn('session.cookie_httponly=1', webmail.render_php_ini())

    def test_optional_client_and_ambient_settings_do_not_change_mail_authority(self):
        config = configuration()
        mail_only = copy.deepcopy(config)
        del mail_only['webmail']
        expected = render_compose(config)
        self.assertEqual(expected['services']['stalwart'], render_compose(mail_only)['services']['stalwart'])
        with patch.dict(os.environ, {'WEBMAIL_PORT': '0.0.0.0:80', 'WEBMAIL_IMAGE': 'untrusted'}):
            self.assertEqual(render_compose(config), expected)
        self.assertNotIn(TEST_KEY, json.dumps(expected))


if __name__ == '__main__':
    unittest.main()
