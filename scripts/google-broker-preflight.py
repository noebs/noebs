#!/usr/bin/env python3
"""Check the public mobile -> Keycloak -> Google registration without signing in.

Only anonymous GETs are made. prompt=none asks Google for a signed-out result;
the broker callback is inspected but never fetched. No codes are exchanged.
"""

import argparse
import base64
import http.client
import http.cookiejar
import json
import re
import secrets
import sys
import urllib.error
import urllib.parse
import urllib.request


class CheckFailure(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def anonymous_getter():
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), NoRedirect()
    )

    def get(url):
        try:
            try:
                response = opener.open(url, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                body = response.read(2 * 1024 * 1024 + 1)
                if len(body) > 2 * 1024 * 1024:
                    raise CheckFailure("Response exceeds the preflight size limit")
                return response.status, response.headers.get("Location", ""), body
        except (OSError, urllib.error.URLError, http.client.HTTPException, ValueError):
            # urllib exceptions can contain the complete state-bearing URL.
            raise CheckFailure("HTTPS request failed; registration remains unverified") from None

    return get


def require(condition, message):
    if not condition:
        raise CheckFailure(message)


def single(query, name):
    values = query.get(name, [])
    require(len(values) == 1 and bool(values[0]), "OAuth parameter missing or duplicated: " + name)
    return values[0]


def location_url(current, location):
    require(bool(location), "Expected an OAuth redirect")
    parsed = urllib.parse.urlsplit(urllib.parse.urljoin(current, location))
    require(not parsed.fragment and not parsed.username and not parsed.password,
            "OAuth redirect contains forbidden URL components")
    return parsed


def google_error(query):
    # Google's browser error URL currently carries a base64 encoded diagnostic.
    # This is not a stable API: unknown formats remain failures, never passes.
    payload = ""
    if len(query.get("authError", [])) == 1:
        try:
            encoded = query["authError"][0]
            require(len(encoded) < 32768, "Google diagnostic exceeds the size limit")
            payload = base64.urlsafe_b64decode(encoded + "===").decode("utf-8", errors="replace")
        except (ValueError, UnicodeError):
            pass
    for code in ("redirect_uri_mismatch", "invalid_client", "deleted_client", "invalid_request",
                 "org_internal", "access_denied", "disallowed_useragent"):
        if code in payload or query.get("error") == [code]:
            return code
    return "unrecognized_provider_error"


def probe(origin, get, report):
    origin = origin.rstrip("/")
    parsed = urllib.parse.urlsplit(origin)
    require(parsed.scheme == "https" and bool(parsed.netloc) and not parsed.path
            and not parsed.query and not parsed.fragment and not parsed.username and not parsed.password,
            "API origin must be an HTTPS origin without credentials, path, query or fragment")
    issuer = origin + "/auth/realms/noebs"
    mobile_callback = origin + "/mobile/oauth/callback"
    broker_callback = issuer + "/broker/google/endpoint"

    status, _, body = get(origin + "/app/config")
    require(status == 200, "Public app configuration is unavailable")
    try:
        oauth = json.loads(body)["oauth"]
        require(oauth["issuer"] == issuer and oauth["client_id"] == "noebs-mobile"
                and oauth["audience"] == "noebs-api" and oauth["redirect_uri"] == mobile_callback,
                "Public mobile OAuth configuration does not match the Noebs contract")
        scopes = oauth["scopes"]
        require(isinstance(scopes, list) and len(scopes) == 2 and set(scopes) == {"openid", "organization:*"},
                "Public mobile scopes must be openid and organization:*")
    except (ValueError, KeyError, TypeError):
        raise CheckFailure("Public app configuration is invalid") from None

    authorization = issuer + "/protocol/openid-connect/auth?" + urllib.parse.urlencode({
        "client_id": oauth["client_id"], "redirect_uri": mobile_callback,
        "response_type": "code", "response_mode": "query", "scope": " ".join(scopes),
        "state": secrets.token_urlsafe(32), "nonce": secrets.token_urlsafe(32),
        "code_challenge": secrets.token_urlsafe(32), "code_challenge_method": "S256",
        "acr_values": "urn:noebs:acr:google", "kc_idp_hint": "google",
    })
    status, location, _ = get(authorization)
    require(status in (302, 303), "Keycloak did not start the Google broker flow")
    broker = location_url(authorization, location)
    require(broker.scheme + "://" + broker.netloc + broker.path == issuer + "/broker/google/login",
            "Keycloak did not redirect to its Google broker login")
    status, location, _ = get(broker.geturl())
    require(status in (302, 303), "Keycloak Google broker did not redirect to Google")
    provider = location_url(broker.geturl(), location)
    require(provider.scheme == "https" and provider.netloc == "accounts.google.com"
            and provider.path == "/o/oauth2/v2/auth", "Unexpected Google authorization endpoint")
    query = urllib.parse.parse_qs(provider.query, keep_blank_values=True)
    require(single(query, "redirect_uri") == broker_callback, "Google broker callback differs from the public issuer")
    require(single(query, "response_type") == "code", "Google broker must request an authorization code")
    require(set(single(query, "scope").split()) == {"openid", "profile", "email"}, "Unexpected Google broker scopes")
    require(not ({"acr_values", "max_age", "client_secret"} & query.keys()),
            "Google broker forwards parameters that must remain inside Keycloak")
    client_id = single(query, "client_id")
    require(len(client_id) < 256 and re.fullmatch(r"[0-9]+-[A-Za-z0-9]+\.apps\.googleusercontent\.com", client_id),
            "Google broker client ID is invalid")
    state = single(query, "state")
    report.update(google_client_id=client_id, broker_redirect_uri=broker_callback)

    # Fresh anonymous cookie jar, no login hint or account selection. Preserve
    # the client, redirect, scopes and state from the real broker request.
    query.pop("login_hint", None)
    query["prompt"] = ["none"]
    current = provider._replace(query=urllib.parse.urlencode(query, doseq=True)).geturl()
    for _ in range(6):
        status, location, _ = get(current)
        require(status in (302, 303), "Google did not return a signed-out OAuth result; registration remains unverified")
        target = location_url(current, location)
        query = urllib.parse.parse_qs(target.query, keep_blank_values=True)
        base = target.scheme + "://" + target.netloc + target.path
        if base == broker_callback:
            require(single(query, "state") == state, "Google response state does not match the broker request")
            require("code" not in query, "Anonymous preflight unexpectedly received an authorization code")
            require(single(query, "error") in {"login_required", "interaction_required", "consent_required", "account_selection_required"},
                    "Google rejected the signed-out registration probe")
            report.update(status="pass", check="google_broker_registration", authenticated_login_verified=False)
            return
        require(target.scheme == "https" and target.netloc == "accounts.google.com", "Unexpected Google response destination")
        if "error" in target.path.lower() or "authError" in query or "error" in query:
            raise CheckFailure("Google rejected the broker registration: " + google_error(query))
        current = target.geturl()
    raise CheckFailure("Google redirect limit exceeded; registration remains unverified")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("api_origin")
    args = parser.parse_args(argv)
    report = {}
    try:
        probe(args.api_origin, anonymous_getter(), report)
    except Exception as error:
        # Only CheckFailure messages are controlled; network and parser errors
        # may include state-bearing URLs. Never print an arbitrary exception.
        report.update(status="fail", error=str(error) if isinstance(error, CheckFailure) else "Preflight failed; registration remains unverified")
        print(json.dumps(report, sort_keys=True))
        return 1
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
