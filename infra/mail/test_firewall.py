from types import SimpleNamespace
import unittest

from firewall import FirewallError, PORTS, reconcile, rules


class MailFirewallTests(unittest.TestCase):
    def test_rules_require_original_public_destination_and_only_mail_ports(self):
        desired = rules('eth0', '213.199.63.78')
        self.assertEqual(len(desired), 4)
        for port, rule in zip(PORTS, desired):
            self.assertEqual(rule[rule.index('--ctorigdst') + 1], '213.199.63.78')
            self.assertEqual(rule[rule.index('--ctorigdstport') + 1], str(port))
            self.assertEqual(rule[rule.index('-i') + 1], 'eth0')
            self.assertEqual(rule[-2:], ['-j', 'RETURN'])
            self.assertNotIn('ACCEPT', rule)

    def test_missing_explicit_interface_or_public_ip_fails(self):
        for interface, address in [('', '213.199.63.78'), ('eth0 bad', '213.199.63.78'), ('eth0', '127.0.0.1')]:
            with self.assertRaises(ValueError):
                rules(interface, address)

    def test_repeated_apply_does_not_duplicate_or_flush_other_rules(self):
        installed = set()
        events = []
        def run(command, **kwargs):
            events.append(command)
            if '-C' in command:
                return SimpleNamespace(returncode=0 if tuple(command[5:]) in installed else 1)
            if '-I' in command:
                installed.add(tuple(command[6:]))
            return SimpleNamespace(returncode=0)
        reconcile('apply', 'eth0', '213.199.63.78', run)
        reconcile('apply', 'eth0', '213.199.63.78', run)
        self.assertEqual(sum('-I' in command for command in events), 4)
        self.assertFalse(any('-F' in command or '-D' in command for command in events))

    def test_failed_rule_check_stops_before_any_rule_mutation(self):
        for action in ('apply', 'remove'):
            for code in (2, 3, 4):
                events = []
                def run(command, **kwargs):
                    events.append(command)
                    return SimpleNamespace(returncode=code if '-C' in command else 0)
                with self.subTest(action=action, code=code), self.assertRaises(FirewallError):
                    reconcile(action, 'eth0', '213.199.63.78', run)
                self.assertFalse(any('-I' in command or '-D' in command for command in events))

    def test_invalid_inputs_do_not_invoke_iptables(self):
        from unittest.mock import Mock
        run = Mock()
        for interface, address in [('eth0;bad', '213.199.63.78'), ('eth0', 3585556302),
                                   ('eth0', '213.199.063.78'), ('eth0', '::1')]:
            with self.subTest(interface=interface, address=address), self.assertRaises(ValueError):
                reconcile('apply', interface, address, run)
        run.assert_not_called()

    def test_removal_touches_only_owned_exceptions(self):
        installed = {tuple(rule) for rule in rules('eth0', '213.199.63.78')}
        events = []
        def run(command, **kwargs):
            events.append(command)
            if '-C' in command:
                return SimpleNamespace(returncode=0 if tuple(command[5:]) in installed else 1)
            if '-D' in command:
                installed.remove(tuple(command[5:]))
            return SimpleNamespace(returncode=0)
        reconcile('remove', 'eth0', '213.199.63.78', run)
        self.assertFalse(installed)
        for command in events:
            self.assertNotIn('-F', command)
            if '-D' in command:
                self.assertIn('--comment', command)
                self.assertTrue(command[command.index('--comment') + 1].startswith('noebs-mail-ingress-'))


if __name__ == '__main__':
    unittest.main()
