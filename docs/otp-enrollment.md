# Authenticator enrollment

The Noebs realm enrolls TOTP with HMAC-SHA-1, six digits, a 30-second period,
look-around window one, and non-reusable codes. Initial OTP enrollment remains
required for local and brokered users. Wallet payment authorization requires
fresh primary authentication (password or the selected identity provider) and
TOTP at `urn:noebs:acr:mfa` (LoA2).

Microsoft Authenticator accepts this enrollment profile. Keycloak 26.7's
[Microsoft authenticator compatibility check](https://github.com/keycloak/keycloak/blob/26.7.0/services/src/main/java/org/keycloak/authentication/otp/MicrosoftAuthenticatorOTPProvider.java)
requires exactly SHA-1, six digits and 30-second TOTP. The previous SHA-256 policy
produced a QR code that could be scanned but did not work with Microsoft
Authenticator. Checking clock alignment alone did not detect this incompatibility.

The desired-state YAML and reconciler validation own the same profile. Deploy
both together through an immutable application image and GitOps promotion;
editing only the live realm is insufficient because reconciliation owns it.
Retain the period, window and single-use policy when correcting the algorithm.

Existing enrolled credentials keep their stored algorithm, digits and period.
Keycloak's [OTP validation](https://github.com/keycloak/keycloak/blob/26.7.0/services/src/main/java/org/keycloak/credential/OTPCredentialProvider.java)
uses those stored values; changing the enrollment algorithm does not rewrite
them. The real Keycloak test deliberately retains a SHA-256 credential to check
compatibility with the corrected enrollment policy. It also drives first-time
mobile enrollment and verifies the resulting SHA-1 credential. Execution
requires the isolated real-Keycloak fixture; a skipped test is not evidence.

After promotion, confirm the live realm policy and start a fresh mobile login.
At **Configure OTP**, add the fresh QR code to Microsoft Authenticator as an
other account and enter the current code from that entry. An enrollment page
from an expired authentication session must be restarted. Never remove unrelated
authenticator entries, bypass OTP, or change a user's stored credential algorithm
to make a code pass. For a failed first enrollment with zero stored OTP
credentials, no server-side credential deletion is necessary.

## Payment authentication time

The `noebs-wallet-authorizer` client has one explicit `noebs-auth-time` protocol
mapper. It reads Keycloak's `AUTH_TIME` session note into the ID token's numeric
`auth_time` claim, using the same source as Keycloak's built-in mapper. It does
not add the claim to access tokens, userinfo or introspection, and does not add
the broader `basic` scope. Mobile and backoffice claim contracts are unchanged.

The transaction authorizer requires this claim to verify fresh authentication.
Removing every direct mapper while assigning only `acr` omitted the timestamp,
even though primary authentication and OTP succeeded. Preserve strict timestamp validation;
repair the issuer's claim configuration instead of accepting a missing value.

Production payment requests use `max_age=0`. Under
[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest),
this requests fresh authentication. Keycloak 26.7's
[cookie authenticator](https://github.com/keycloak/keycloak/blob/26.7.0/services/src/main/java/org/keycloak/authentication/authenticators/browser/CookieAuthenticator.java)
resets the current authentication level when reauthentication is required, so
the local password or selected broker is required again before TOTP. The realm's reusable
LoA1 policy does not override this explicit request. The real regression uses
the production request parameters, completes two authorizations in the same
browser session using distinct current codes, verifies both signed ID tokens,
and requires fresh authentication timestamps that advance between requests.
