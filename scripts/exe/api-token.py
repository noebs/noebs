#!/usr/bin/env python3
"""Sign a one-hour exe.dev token with an already authorized SSH identity."""
import base64
import json
import subprocess
import sys
import time


def encode(value):
    return base64.urlsafe_b64encode(value).decode().rstrip("=")


payload = json.dumps({"exp": int(time.time()) + 3600, "cmds": ["ls", "new", "resize", "rm", "share set-private", "share set-public"]}, separators=(",", ":")).encode()
signed = subprocess.run(["ssh-keygen", "-Y", "sign", "-f", sys.argv[1], "-n", "v0@exe.dev"], input=payload, capture_output=True, check=True).stdout
signature = base64.b64decode(b"".join(signed.splitlines()[1:-1]))
print("exe0." + encode(payload) + "." + encode(signature))
