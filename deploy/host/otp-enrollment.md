# Authenticator enrollment

The Noebs realm enrolls TOTP with HMAC-SHA-1, six digits, a 30-second period,
look-around window one, and non-reusable codes. Initial OTP enrollment remains
required, and wallet payment authorization still requires a fresh Google/TOTP
LoA2 authorization.

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
compatibility with the corrected enrollment policy. Its execution still
requires the isolated real-Keycloak fixture; a skipped test is not evidence.

After promotion, confirm the live realm policy and start a fresh mobile login.
At **Configure OTP**, add the fresh QR code to Microsoft Authenticator as an
other account and enter the current code from that entry. An enrollment page
from an expired authentication session must be restarted. Never remove unrelated
authenticator entries, bypass OTP, or change a user's stored credential algorithm
to make a code pass. For a failed first enrollment with zero stored OTP
credentials, no server-side credential deletion is necessary.
