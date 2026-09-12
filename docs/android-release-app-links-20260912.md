# Android release certificate and App Links — 12 September 2026

The tracked NoEBS edge configuration now authorizes the existing managed TutiPay
release signing certificate for HTTPS App Links on `api.noebs.sd`. This adds one
public certificate fingerprint; it does not create, rotate or replace a key.
The two previously configured certificates remain authorized.

| Identity | SHA-256 |
| --- | --- |
| Existing published certificate, retained | `B4:45:C2:79:FE:FB:B0:95:AA:33:4F:67:42:4D:EA:6B:52:77:38:EA:FF:A5:EF:FB:80:B5:E2:F5:9B:66:1C:AE` |
| Current alpha/debug certificate, retained | `BB:1F:DB:0B:76:AE:89:D6:B6:BD:7A:D4:A1:5F:85:59:60:16:55:48:73:8E:E4:B8:DC:89:4A:8F:BC:1A:AE:F5` |
| Existing managed release certificate, added | `08:16:D2:E0:36:91:A9:FB:4C:39:A4:49:DB:18:B6:FD:DB:35:7C:52:83:96:1F:1A:19:34:D7:6D:2A:99:0A:A7` |

The release fingerprint was checked against the app repository's public
`secrets/android-certificates.json` and `docs/android-signing.md`. The target
remains `com.tutipay.app.alpha`; no additional Android package or dev signing
certificate is authorized. The served statement keeps the existing
`delegate_permission/common.handle_all_urls` relation, JSON content type and
HTTP 200 response.

## Sign-in and service scope

The app's active sign-in uses the browser, NoEBS's `noebs-mobile` OIDC client,
PKCE, and the existing `https://api.noebs.sd/mobile/oauth/callback` redirect.
Google authentication is brokered by the server. The active app flow does not
use native Google Sign-In or Firebase Authentication. Payment authorization
also uses the existing NoEBS browser flow.

For the current build with telemetry and push disabled, authorizing the release
certificate at the HTTPS origin is the required signing-related change for
Android to return those browser links to the release APK. The OIDC client,
Google broker configuration and redirect URI remain unchanged. This conclusion
does not establish registration for Play distribution, native Google sign-in,
or future Firebase features. The app's existing Firebase Android OAuth client
has a different historical SHA-1; it is not used by the current browser sign-in.

## Existing-device upgrade boundary

App Links authorization does not change Android's package-signature update
rules. The Samsung installation signed by the current alpha/debug certificate
must receive an APK signed by that same certificate and with a newer version
code. A managed-release-signed APK cannot replace it under the same package ID.
Do not uninstall or clear the existing app to work around that mismatch.

The optimized release build is suitable for a separate clean installation,
such as an isolated emulator, once its signature, build and App Links are
verified. Both artifacts can be published with clear certificate labels; the
alpha/debug artifact remains the update path for existing alpha installations.
This change does not rename a package or alter physical-device data.

## Verification and rollout

The focused source contract parses the actual Caddy JSON statement and checks
its target, relation and exact three-certificate set:

```sh
# From the NoEBS repository's cli directory; this isolated contract needs no DB.
go test android_assetlinks_test.go -v
```

The normal CLI test suite includes the same test. The release smoke check must
expect the same three-certificate list. The edge Kustomize ConfigMap name changes
with the Caddyfile content, so deployment follows the existing Argo edge release
process. Its existing `Recreate` strategy entails a brief edge restart; this
change does not introduce another edge service or manual deployment authority.

After that coordinated rollout, capture the public response under `.state/`:

```sh
mkdir -p .state/android-release-links
curl --fail --silent --show-error \
  https://api.noebs.sd/.well-known/assetlinks.json \
  > .state/android-release-links/assetlinks.json
```

Verify the APK with `apksigner verify --print-certs` and confirm that its SHA-256
matches the intended profile. On a separately selected clean Android test
installation, request and inspect system link verification:

```sh
adb -s "$ISOLATED_SERIAL" shell pm verify-app-links --re-verify com.tutipay.app.alpha
adb -s "$ISOLATED_SERIAL" shell pm get-app-links com.tutipay.app.alpha
```

Then complete browser sign-in and payment authorization acceptance using that
APK. Source-contract success is not live deployment or authenticated-device
acceptance evidence. Record the deployed revision, actual HTTPS response and
Android verification result in the coordinated release receipt.
