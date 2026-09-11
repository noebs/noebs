import base64
import contextlib
import http.client
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest import mock
import urllib.error
import urllib.parse


spec = importlib.util.spec_from_file_location(
    "google_broker_preflight", Path(__file__).resolve().parents[1] / "google-broker-preflight.py"
)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)

ORIGIN = "https://api.example.test"
ISSUER = ORIGIN + "/auth/realms/noebs"
CALLBACK = ISSUER + "/broker/google/endpoint"
CLIENT = "123-example.apps.googleusercontent.com"
PRIVATE_STATE = "state-must-never-appear-in-diagnostics"


class GoogleBrokerPreflightTest(unittest.TestCase):
    def run_probe(self, provider_reply, *, scopes=None, broker_redirect=None, provider_changes=None):
        requested = []
        report = {}

        def get(url):
            requested.append(url)
            parsed = urllib.parse.urlsplit(url)
            if parsed.netloc == "accounts.google.com":
                query = urllib.parse.parse_qs(parsed.query)
                self.assertEqual(query["prompt"], ["none"])
                self.assertEqual(query["redirect_uri"], [CALLBACK])
                self.assertEqual(query["client_id"], [CLIENT])
                return provider_reply(query)
            if parsed.path == "/app/config":
                return 200, "", json.dumps({"oauth": {
                    "issuer": ISSUER, "client_id": "noebs-mobile", "audience": "noebs-api",
                    "redirect_uri": ORIGIN + "/mobile/oauth/callback",
                    "scopes": scopes if scopes is not None else ["openid", "organization:*"],
                }}).encode()
            if parsed.path.endswith("/protocol/openid-connect/auth"):
                return 303, broker_redirect or ISSUER + "/broker/google/login?session=private", b""
            if parsed.path.endswith("/broker/google/login"):
                query = {"client_id": CLIENT, "redirect_uri": CALLBACK, "response_type": "code",
                         "scope": "openid profile email", "state": PRIVATE_STATE}
                query.update(provider_changes or {})
                return 303, "https://accounts.google.com/o/oauth2/v2/auth?" + urllib.parse.urlencode(query), b""
            self.fail("Probe fetched an unexpected endpoint, including possibly the broker callback")

        try:
            preflight.probe(ORIGIN, get, report)
        except preflight.CheckFailure as error:
            return report, requested, str(error)
        return report, requested, None

    def test_registered_client_stops_before_signed_out_callback(self):
        for error in ("login_required", "interaction_required", "consent_required", "account_selection_required"):
            with self.subTest(error=error):
                report, requested, failure = self.run_probe(
                    lambda query: (302, CALLBACK + "?" + urllib.parse.urlencode({
                        "error": error, "state": query["state"][0]}), b""))
                self.assertIsNone(failure)
                self.assertEqual(report["status"], "pass")
                self.assertFalse(report["authenticated_login_verified"])
                self.assertEqual(len(requested), 4)
                self.assertNotIn(PRIVATE_STATE, json.dumps(report))

    def test_google_error_redirect_is_failure_even_if_its_page_would_be_http_200(self):
        payload = base64.urlsafe_b64encode(b"\x12redirect_uri_mismatch\x00private-provider-detail").decode().rstrip("=")
        report, requested, failure = self.run_probe(lambda _: (302,
            "https://accounts.google.com/signin/oauth/error?authError=" + payload, b""))
        self.assertIn("redirect_uri_mismatch", failure)
        self.assertEqual(report["broker_redirect_uri"], CALLBACK)
        self.assertEqual(len(requested), 4)
        self.assertNotIn("private-provider-detail", failure)

    def test_unknown_google_error_format_never_passes(self):
        _, _, failure = self.run_probe(lambda _: (302,
            "https://accounts.google.com/signin/oauth/error?authError=unknown", b""))
        self.assertIn("unrecognized_provider_error", failure)

    def test_google_login_page_is_not_proof_of_registered_callback(self):
        _, _, failure = self.run_probe(lambda _: (200, "", b"<h1>Sign in</h1>"))
        self.assertIn("unverified", failure)

    def test_wrong_state_duplicate_state_and_success_code_are_rejected(self):
        for query in ("error=login_required&state=wrong",
                      "error=login_required&state=" + PRIVATE_STATE + "&state=" + PRIVATE_STATE,
                      "error=login_required&state=" + PRIVATE_STATE + "&code=private-code"):
            with self.subTest(query=query.split("&")[0]):
                _, requested, failure = self.run_probe(lambda _: (302, CALLBACK + "?" + query, b""))
                self.assertIsNotNone(failure)
                self.assertEqual(len(requested), 4)
                self.assertNotIn(PRIVATE_STATE, failure)
                self.assertNotIn("private-code", failure)

    def test_untrusted_redirects_are_never_fetched(self):
        for destination in ("https://untrusted.example/", "http://accounts.google.com/",
                            "https://user:password@accounts.google.com/", CALLBACK + "#state"):
            with self.subTest(destination=destination):
                _, requested, failure = self.run_probe(lambda _: (302, destination, b""))
                self.assertIsNotNone(failure)
                self.assertEqual(len(requested), 4)
        _, requested, failure = self.run_probe(lambda _: self.fail("Google should not be reached"),
            broker_redirect="https://untrusted.example/broker")
        self.assertIsNotNone(failure)
        self.assertEqual(len(requested), 2)

    def test_old_mobile_scope_bug_fails_before_authorization(self):
        _, requested, failure = self.run_probe(lambda _: self.fail("Google should not be reached"),
            scopes=["openid", "profile", "email", "organization:*"])
        self.assertIn("mobile scopes", failure)
        self.assertEqual(len(requested), 1)

    def test_broker_callback_drift_and_local_auth_parameters_fail_before_google(self):
        for changes in ({"redirect_uri": ORIGIN + "/mobile/oauth/callback"},
                        {"acr_values": "urn:noebs:acr:google"}, {"client_secret": "private-secret"}):
            with self.subTest(keys=list(changes)):
                _, requested, failure = self.run_probe(lambda _: self.fail("Google should not be reached"),
                    provider_changes=changes)
                self.assertIsNotNone(failure)
                self.assertNotIn("private-secret", failure)
                self.assertEqual(len(requested), 3)

    def test_network_error_does_not_disclose_request_url(self):
        for network_error in (urllib.error.URLError("https://accounts.google.com/?state=" + PRIVATE_STATE),
                              http.client.BadStatusLine(PRIVATE_STATE), http.client.IncompleteRead(PRIVATE_STATE.encode())):
            with self.subTest(error=type(network_error).__name__):
                with mock.patch.object(urllib.request.OpenerDirector, "open", side_effect=network_error):
                    with self.assertRaises(preflight.CheckFailure) as failure:
                        preflight.anonymous_getter()("https://accounts.google.com/?state=" + PRIVATE_STATE)
                self.assertNotIn(PRIVATE_STATE, str(failure.exception))

    def test_malformed_client_id_is_never_reflected_into_diagnostics(self):
        report, _, failure = self.run_probe(lambda _: self.fail("Google should not be reached"),
            provider_changes={"client_id": PRIVATE_STATE + ".apps.googleusercontent.com"})
        self.assertIsNotNone(failure)
        self.assertNotIn(PRIVATE_STATE, json.dumps(report) + failure)

    def test_unexpected_failure_does_not_emit_raw_exception_or_traceback(self):
        output = io.StringIO()
        with mock.patch.object(preflight, "probe", side_effect=RuntimeError(PRIVATE_STATE)):
            with contextlib.redirect_stdout(output):
                code = preflight.main([ORIGIN])
        self.assertEqual(code, 1)
        self.assertNotIn(PRIVATE_STATE, output.getvalue())
        self.assertEqual(json.loads(output.getvalue())["status"], "fail")


if __name__ == "__main__":
    unittest.main()
