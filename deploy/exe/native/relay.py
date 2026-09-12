#!/usr/bin/env python3
"""Restricted FSPIOP relay over the authenticated WireGuard peer.

It does not terminate JWS, change a message body, or expose hub administration.
The peer bootstrap restricts inbound interface/address/port in addition to these
path and participant-header checks.
"""
import argparse
import http.client
import http.server
import re
import socket
import hmac
from pathlib import Path

ID = r"[0-9a-fA-F-]{36}"
ROUTES = (
    (re.compile(r"^/(?:participants|parties)/MSISDN/249[0-9]{9}(?:/error)?$"), "als"),
    (re.compile(r"^/quotes(?:/" + ID + r"(?:/error)?)?$"), "quotes"),
    (re.compile(r"^/transfers(?:/" + ID + r"(?:/error)?)?$"), "transfers"),
)
HOP = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "host"}
HUB_SOURCES = {"Hub", "hub", "switch"}  # Exact configured FSP IDs, case-sensitive.

def route(path):
    if "?" in path or "#" in path:
        return None
    return next((service for pattern, service in ROUTES if pattern.fullmatch(path)), None)

def handler(targets, direction, accept_secret=None, send_secret=None):
    class Relay(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        def log_message(self, *_):
            pass  # Never log party identifiers or protocol payloads.
        def setup(self):
            super().setup()
            self.connection.settimeout(15)
        def forward(self):
            service = route(self.path)
            source = self.headers.get("FSPIOP-Source", "")
            expected = {"noebs"} if direction == "hub" else {"bankone", "walletone"} | HUB_SOURCES
            duplicate = any(len(self.headers.get_all(key, [])) > 1 for key in ("FSPIOP-Source", "FSPIOP-Destination", "Content-Length", "Content-Type", "X-Noebs-Relay"))
            invalid_final = direction == "sdk" and self.command == "PATCH" and (service != "transfers" or source not in HUB_SOURCES)
            invalid_secret = accept_secret is not None and not hmac.compare_digest(self.headers.get("X-Noebs-Relay", ""), accept_secret)
            if service is None or source not in expected or duplicate or invalid_final or invalid_secret or self.headers.get("Transfer-Encoding"):
                self.send_error(403)
                self.close_connection = True
                return
            try:
                size = int(self.headers.get("Content-Length", "0"))
                if not 0 <= size <= 1 << 20:
                    raise ValueError("body size")
                body = self.rfile.read(size)
                if len(body) != size:
                    raise ValueError("truncated request")
                conn = http.client.HTTPConnection(targets[service], timeout=20)
                headers = {key: value for key, value in self.headers.items() if key.lower() not in HOP | {"content-length", "x-noebs-relay"}}
                headers["Content-Length"] = str(len(body))
                if send_secret is not None:
                    headers["X-Noebs-Relay"] = send_secret
                conn.request(self.command, self.path, body=body, headers=headers)
                response = conn.getresponse()
                raw = response.read((1 << 20) + 1)
                if len(raw) > 1 << 20:
                    raise ValueError("response size")
                self.send_response(response.status)
                for key, value in response.getheaders():
                    if key.lower() not in HOP | {"content-length"}:
                        self.send_header(key, value)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
                conn.close()
            except (ValueError, OSError, http.client.HTTPException):
                self.send_error(502)
                self.close_connection = True
        do_GET = do_POST = do_PUT = do_PATCH = forward
    return Relay

if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--listen", required=True, choices=["172.30.250.1", "172.30.250.7", "0.0.0.0"])
    p.add_argument("--direction", choices=["hub", "sdk"], required=True)
    p.add_argument("--als", required=True)
    p.add_argument("--quotes", required=True)
    p.add_argument("--transfers", required=True)
    p.add_argument("--accept-auth-file")
    p.add_argument("--send-auth-file")
    a = p.parse_args()
    accept = Path(a.accept_auth_file).read_text().strip() if a.accept_auth_file else None
    send = Path(a.send_auth_file).read_text().strip() if a.send_auth_file else None
    if (accept is not None and len(accept) < 40) or (send is not None and len(send) < 40):
        raise ValueError("invalid relay credential")
    http.server.ThreadingHTTPServer((a.listen, 4040), handler(vars(a), a.direction, accept, send)).serve_forever()
