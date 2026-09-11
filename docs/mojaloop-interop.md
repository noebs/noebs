# Deployed noebs Mojaloop participant

The synthetic SDG profile is deployed at `https://api.noebs.sd`, with
`tenant-mojaloop` bound explicitly to DFSP `noebs`. The Android app uses the
authenticated noebs wallet API; noebs retains its customer ledger and controls
admission, holds, recovery and balanced posting. The official SDK handles native
Mojaloop protocol transport in the wallet-worker pod.

The source release is `08a771d74f5f504ac8597d987b401ed8390a3937`, promoted by
`6a51a8a468f89863c9dc3d7b98e5af18121a2ec1`. Both foundation-managed Argo
applications were Synced/Healthy on 11 September 2026. Images:

- Application: `ghcr.io/noebs/noebs@sha256:5c4d73c6a8163df5383a282c334f2ad10c9946f9eb7391f3cbf537d9b3f59f29`
- SDK: `ghcr.io/noebs/noebs@sha256:baf1648ac2b5945e7baaad60df02557cfc47bdbb2fe364f94f51b1d8c6b47eef`
- Official SDK source: v24.19.5, `6594dc5689a95dffece23d481a407e4368d80a96`.

The SDK outbound API and noebs backend bind to pod loopback, ports 4001 and
4002. Native inbound port 4000 is reached through restricted, authenticated
WireGuard relays in `~/src/mojaloop/services/noebs-sdk`. Native hub source is
exactly `Hub`; participant IDs are case-sensitive. Network isolation and source
checks are part of the synthetic trust boundary. Production JWS and participant
mTLS onboarding are not implemented.

## Contract and ownership

The profile supports MSISDN, SDG with explicit minor-unit version, SEND,
consumer-payer TRANSFER, and zero fees. Wallet API amounts are strings containing
integer minor units. New SDK protocol amounts are decimal strings without
trailing fractional zeroes; timestamps have exactly three fractional digits.
The boundary gives an incoming quote two minutes if its optional native expiry
is omitted. Persisted responses and expiry win retries; received prepare terms
are never rewritten.

Quotes carry an immutable DFSP `homeTransactionId`. Monetary admission uses the
distinct `wallet.interop.transfer` authorization bound to tenant, owner, quote
and command key. A durable owner closure serializes abandoned-quote handling
against delayed admission. SQL records original request/prepare/fulfilment
data, leased dispatch and authoritative outcome receipts. Runtime credentials
admit commands; worker credentials reconcile them.

Only correlated native COMMITTED or ABORTED outcomes settle an obligation.
Timeout, 404, process restart and post-submission quote expiry retain the hold.
Committed incoming money goes to suspense if its destination becomes ineligible.
Conflicting terminal evidence is quarantined. Some outages spanning prepare
expiry can remain unresolved and require operator reconciliation.

## Executed verification

The live synthetic operator run sent SDG 1.00 from noebs to bankone and SDG 1.11
from bankone to noebs. Exact native MySQL transfer IDs, amounts and COMMITTED
states were checked independently. Noebs recorded exactly two balanced payment
journals. Duplicate command keys returned the original transfers and left the
entire customer-ledger snapshot unchanged.

A prior SDG 1.23 outgoing failed native liquidity checks. Its hold remained
reserved through a callback outage and deployment. Normal native GET recovery
then observed ABORTED and released the hold without a payment journal. The same
recovery mechanism credited the committed incoming transfer once. No operator
wrote a terminal outcome or altered balances to obtain these results.

Native position and customer wallet funds are separate. Noebs began with zero
position and settlement, plus an explicit NDC. Its seeded customer opening
journal did not create hub liquidity. The incoming 1.11 funded the 1.00 payout;
no additional native funds-in or seed refill was performed.

Read-only verification scripts, immutable receipts, SDK schema checks and the
separate engineering review are in `~/src/mojaloop/evidence/noebs-*` and
`~/src/mojaloop/scripts/verify-noebs.py`. Android integration and review are in
`~/src/app/docs/mojaloop-integration.md` and `mojaloop-integration-review.md`.
Real PostgreSQL runtime/worker tests, concurrency tests, protocol validation,
release checks and Android assembly/money tests passed.

The original native checks above used synthetic operator identities. The selected
Google user has since completed sign-in and authenticator enrollment on the
installed Samsung build, received exactly `tenant-mojaloop:user`, and created
their own profile and wallet. Membership and ownership were checked separately;
no operator wallet was reassigned. Synthetic alias `249900000023` now routes to
that personal wallet. Native transfer `f0255e34-d53b-4f71-83c0-9bb976b07ee6`
credited SDG 1.11 from bankone with one balanced journal. A same-key replay left
both balances unchanged. The authenticated outgoing Android payment remains
pending and is separate from this operator funding step.

The live enrollment policy is SHA-1, six digits, 30 seconds, window one and no
code reuse, with required enrollment and OTP retained in both LoA2 flows. The
payment authorizer now maps Keycloak's `AUTH_TIME` session note to an ID-token-only
`auth_time` long claim. Its exact `acr` default scope and mobile/backoffice claim
contracts are preserved. This fixes the missing claim without weakening fresh
payment authorization. Production `max_age=0` requests repeat broker authentication
and require OTP even when the browser already has a session.

An isolated test against pinned Keycloak 26.7.0 passed fresh Microsoft-compatible
enrollment, two legacy SHA-256 OTP authorizations with signed tokens and advancing
authentication times, hostile configuration repair and final convergence. An
independent post-promotion review passed all 18 live authority checks. Sanitized
release, funding and authentication evidence is in
`~/src/app/docs/evidence/mojaloop-payment-auth-20260911.json` and
`~/src/app/docs/evidence/mojaloop-personal-funding-20260911.json`.
