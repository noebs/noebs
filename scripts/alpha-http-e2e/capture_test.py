from __future__ import annotations

import importlib.util
import socket
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("capture.py")
SPEC = importlib.util.spec_from_file_location("alpha_capture", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
capture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(capture)


class CaptureServerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.server = capture.create_server("127.0.0.1", 0, "sms-secret", "read-secret")
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host, port = self.server.server_address
        self.base_url = f"http://{host}:{port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, path: str, token: str = "", method: str = "GET") -> tuple[int, str]:
        headers = {"Authorization": f"Bearer {token}"} if token else {}
        request = urllib.request.Request(self.base_url + path, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=2) as response:
                return response.status, response.read().decode("utf-8")
        except urllib.error.HTTPError as error:
            return error.code, error.read().decode("utf-8")

    def test_otp_capture_is_authenticated_and_one_time(self) -> None:
        query = urllib.parse.urlencode(
            {
                "api_key": "sms-secret",
                "to": "249912345678",
                "sms": "Your one-time access code is: 123456. Do not share it.",
            }
        )
        self.assertEqual((204, ""), self.request("/sms?" + query))
        self.assertEqual((401, ""), self.request("/otp", "wrong"))
        self.assertEqual((200, "123456"), self.request("/otp", "read-secret"))
        self.assertEqual((404, ""), self.request("/otp", "read-secret"))

    def test_forbidden_side_effect_guard_counts_requests(self) -> None:
        self.assertEqual((204, ""), self.request("/assert-zero", "read-secret"))
        self.assertEqual((503, ""), self.request("/guard/ebs-adapter/consumer/balance", method="POST"))
        self.assertEqual((503, ""), self.request("/guard/unknown", method="TRACE"))
        self.assertEqual((409, ""), self.request("/assert-zero", "read-secret"))

    def test_duplicate_otp_delivery_is_rejected(self) -> None:
        query = urllib.parse.urlencode({"api_key": "sms-secret", "sms": "Code 123456"})
        self.assertEqual((204, ""), self.request("/sms?" + query))
        self.assertEqual((204, ""), self.request("/sms?" + query))
        self.assertEqual((409, ""), self.request("/otp", "read-secret"))
        self.assertEqual((404, ""), self.request("/otp", "read-secret"))

    def test_grpc_connection_is_counted_as_forbidden(self) -> None:
        state = capture.CaptureState("sms-secret", "read-secret")
        guard = capture.create_guard_server("127.0.0.1", 0, state)
        thread = threading.Thread(target=guard.serve_forever, daemon=True)
        thread.start()
        try:
            with socket.create_connection(guard.server_address, timeout=2):
                pass
            for _ in range(20):
                with state.lock:
                    if state.forbidden_requests == 1:
                        break
                threading.Event().wait(0.01)
            with state.lock:
                self.assertEqual(1, state.forbidden_requests)
        finally:
            guard.shutdown()
            guard.server_close()
            thread.join(timeout=2)

    def test_relay_preserves_http_status_and_body(self) -> None:
        status, body = capture.relay_request(
            {"method": "GET", "path": "/health"},
            self.base_url,
        )
        self.assertEqual((204, ""), (status, body))


if __name__ == "__main__":
    unittest.main()
