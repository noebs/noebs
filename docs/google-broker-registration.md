# Google broker registration

Noebs mobile, backoffice and wallet authorization authenticate through the
Keycloak Google identity provider. Google returns to the Keycloak broker, then
Keycloak returns to the individual Noebs client callback. Google registration
and Keycloak client redirect registrations are separate configuration surfaces.

For the current `https://api.noebs.sd` deployment, Google must authorize this
exact redirect URI on the broker's **Web application** OAuth client:

```text
https://api.noebs.sd/auth/realms/noebs/broker/google/endpoint
```

The deployed public client ID, verified on 11 September 2026 against both the
outgoing request and the Keycloak reconciler credential, is:

```text
1055375696469-a89cqh1trfo2rstc7etc1fjf5mvqn32s.apps.googleusercontent.com
```

Open [Google Auth Platform Clients for project 1055375696469](https://console.cloud.google.com/auth/clients?project=1055375696469),
select that client, and add the URI under **Authorized redirect URIs**, preserving
the existing entries. The Android app's Firebase project and signing certificate
do not control this server broker callback. The reconciler's Google client
secret authenticates token exchanges; it cannot edit Google Cloud registration.

Google requires an exact URI match. Changes can take five minutes to a few hours
to propagate. See [Google's client registration documentation](https://support.google.com/cloud/answer/15549257?hl=en).
Adding this URI does not require rotating the client secret or rebuilding Android.

Run the public registration preflight after the Console change:

```sh
python3 scripts/google-broker-preflight.py https://api.noebs.sd
```

The check reads the deployed mobile OAuth contract, traces the Keycloak broker,
and sends Google's actual client ID, callback and scopes with `prompt=none` in
an anonymous session. It passes only when Google redirects a signed-out OAuth
result with matching state to the exact broker callback. It never fetches that
callback or exchanges a code. Unknown provider responses fail as unverified.
Diagnostics include only the public client ID, callback, and fixed errors;
state, nonce, codes, cookies and provider error payloads are never printed.

The alpha post-deploy smoke invokes this check. Its earlier check only verified
that Keycloak redirected to Google, which did not catch `redirect_uri_mismatch`.
The new preflight does not establish that broker token exchange, Google account
policy, TOTP, organization membership or authenticated payment works. Complete
the device login, provision the resulting immutable subject using the
[membership operation](../kubernetes/operations/README.md), then verify the
authenticated wallet and payment separately.
