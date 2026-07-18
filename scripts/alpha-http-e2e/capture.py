#!/usr/bin/env python3
"""Private, in-memory OTP capture and forbidden-side-effect guard."""

from __future__ import annotations

import hmac
import json
import os
import re
import socketserver
import sys
import threading
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


OTP_PATTERN = re.compile(r"(?<!\d)(\d{6})(?!\d)")
SMS_KEY_FILE = Path(os.environ.get("NOEBS_E2E_SMS_KEY_FILE", "/run/secrets/otp_sms_key"))
READ_TOKEN_FILE = Path(os.environ.get("NOEBS_E2E_READ_TOKEN_FILE", "/run/secrets/otp_read_token"))
CAPTURE_URL = os.environ.get("NOEBS_E2E_CAPTURE_URL", "http://127.0.0.1:8080")
API_URL = os.environ.get("NOEBS_E2E_API_URL", "http://api-gateway:8080")


def read_secret(path: Path) -> str:
    value = path.read_text(encoding="utf-8").strip()
    if not value:
        raise RuntimeError(f"empty secret file: {path}")
    return value


class CaptureState:
    def __init__(self, sms_key: str, read_token: str) -> None:
        self.sms_key = sms_key
        self.read_token = read_token
        self.otps: list[str] = []
        self.forbidden_requests = 0
        self.lock = threading.Lock()


def handler_for(state: CaptureState) -> type[BaseHTTPRequestHandler]:
    class CaptureHandler(BaseHTTPRequestHandler):
        server_version = "NoebsAlphaCapture/1"

        def log_message(self, _format: str, *_args: object) -> None:
            # Request URLs contain the SMS body, so access logging is forbidden.
            return

        def send_empty(self, status: int) -> None:
            self.send_response(status)
            self.send_header("Cache-Control", "no-store")
            self.end_headers()

        def send_text(self, status: int, value: str) -> None:
            encoded = value.encode("utf-8")
            self.send_response(status)
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def read_authorized(self) -> bool:
            prefix = "Bearer "
            authorization = self.headers.get("Authorization", "")
            if not authorization.startswith(prefix):
                return False
            return hmac.compare_digest(authorization[len(prefix) :], state.read_token)

        def reject_forbidden(self) -> None:
            with state.lock:
                state.forbidden_requests += 1
            self.send_empty(503)

        def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            parsed = urllib.parse.urlsplit(self.path)
            if parsed.path == "/health":
                self.send_empty(204)
                return

            if parsed.path == "/sms":
                query = urllib.parse.parse_qs(parsed.query, keep_blank_values=True)
                supplied_key = query.get("api_key", [""])[0]
                if not hmac.compare_digest(supplied_key, state.sms_key):
                    self.send_empty(401)
                    return
                match = OTP_PATTERN.search(query.get("sms", [""])[0])
                if match is None:
                    self.send_empty(400)
                    return
                with state.lock:
                    state.otps.append(match.group(1))
                self.send_empty(204)
                return

            if parsed.path == "/otp":
                if not self.read_authorized():
                    self.send_empty(401)
                    return
                with state.lock:
                    deliveries = state.otps
                    state.otps = []
                if not deliveries:
                    self.send_empty(404)
                    return
                if len(deliveries) != 1:
                    self.send_empty(409)
                    return
                self.send_text(200, deliveries[0])
                return

            if parsed.path == "/assert-zero":
                if not self.read_authorized():
                    self.send_empty(401)
                    return
                with state.lock:
                    count = state.forbidden_requests
                self.send_empty(204 if count == 0 else 409)
                return

            self.reject_forbidden()

        def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def do_PUT(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def do_PATCH(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def do_DELETE(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def do_HEAD(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def do_OPTIONS(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
            self.reject_forbidden()

        def __getattr__(self, name: str):
            if name.startswith("do_"):
                return self.reject_forbidden
            raise AttributeError(name)

    return CaptureHandler


def create_server(host: str, port: int, sms_key: str, read_token: str) -> ThreadingHTTPServer:
    return ThreadingHTTPServer((host, port), handler_for(CaptureState(sms_key, read_token)))


def create_guard_server(host: str, port: int, state: CaptureState) -> socketserver.ThreadingTCPServer:
    class GuardHandler(socketserver.BaseRequestHandler):
        def handle(self) -> None:
            with state.lock:
                state.forbidden_requests += 1

    class GuardServer(socketserver.ThreadingTCPServer):
        allow_reuse_address = True
        daemon_threads = True

    return GuardServer((host, port), GuardHandler)


def protected_request(path: str) -> tuple[int, str]:
    token = read_secret(READ_TOKEN_FILE)
    request = urllib.request.Request(
        CAPTURE_URL + path,
        headers={"Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(request, timeout=3) as response:
            return response.status, response.read().decode("utf-8")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8")


def relay_request(payload: dict[str, object], api_url: str = API_URL) -> tuple[int, str]:
    method = str(payload.get("method", "")).strip().upper()
    path = str(payload.get("path", "")).strip()
    if not method or not path.startswith("/") or path.startswith("//"):
        raise ValueError("invalid relay request")

    headers = {"Accept": "application/json"}
    tenant = str(payload.get("tenant", "")).strip()
    authorization = str(payload.get("authorization", "")).strip()
    if tenant:
        headers["X-Tenant-ID"] = tenant
    if authorization:
        headers["Authorization"] = f"Bearer {authorization}"

    body = str(payload.get("body", ""))
    data = None
    if body:
        data = body.encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(
        api_url.rstrip("/") + path,
        data=data,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return response.status, response.read().decode("utf-8")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8")


def run_client(command: str) -> int:
    if command == "health":
        try:
            with urllib.request.urlopen(CAPTURE_URL + "/health", timeout=3) as response:
                return 0 if response.status == 204 else 1
        except (OSError, urllib.error.URLError):
            return 1

    if command == "read":
        status, body = protected_request("/otp")
        if status != 200 or OTP_PATTERN.fullmatch(body) is None:
            return 1
        sys.stdout.write(body)
        return 0

    if command == "expect-empty":
        status, _ = protected_request("/otp")
        return 0 if status == 404 else 1

    if command == "assert-zero":
        status, _ = protected_request("/assert-zero")
        return 0 if status == 204 else 1

    if command == "relay":
        try:
            payload = json.load(sys.stdin)
            if not isinstance(payload, dict):
                return 1
            status, body = relay_request(payload)
        except (OSError, ValueError, urllib.error.URLError, json.JSONDecodeError):
            return 1
        sys.stdout.write(json.dumps({"status": status, "body": body}, separators=(",", ":")))
        return 0

    return 2


def main() -> int:
    command = sys.argv[1] if len(sys.argv) == 2 else ""
    if command == "serve":
        state = CaptureState(read_secret(SMS_KEY_FILE), read_secret(READ_TOKEN_FILE))
        server = ThreadingHTTPServer(("0.0.0.0", 8080), handler_for(state))
        guard = create_guard_server("0.0.0.0", 9090, state)
        guard_thread = threading.Thread(target=guard.serve_forever, daemon=True)
        guard_thread.start()
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        finally:
            server.server_close()
            guard.shutdown()
            guard.server_close()
            guard_thread.join(timeout=2)
        return 0
    return run_client(command)


if __name__ == "__main__":
    raise SystemExit(main())
