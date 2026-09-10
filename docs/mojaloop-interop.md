# Deployed noebs Mojaloop participant

The synthetic SDG profile is deployed at `https://api.noebs.sd`, with
`tenant-mojaloop` bound explicitly to DFSP `noebs`. The Android app uses the
authenticated noebs wallet API; noebs retains its customer ledger and controls
admission, holds, recovery and balanced posting. The official SDK handles native
Mojaloop protocol transport in the wallet-worker pod.

The source release is `d70c52f64e1f5800fce63a7ea111a234b4812dc1`, promoted by
`57e6223d5a2151a3781d20aa16fb830751c8c015`. Both foundation-managed Argo
applications were Synced/Healthy on 11 September 2026. Images:

- Application: `ghcr.io/noebs/noebs@sha256:8976c1b82472b8f12a1b04d02a35ffef95d0f863c32e7ce644d3bb469cdd190c`
- SDK: `ghcr.io/noebs/noebs@sha256:b86a0045377e9cbfd8330533cbd4639608e813edd69950eda41045d6cef2053e`
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

This is an operator integration result using synthetic identities. The released
Android alpha still needs the intended Google user's membership, wallet and
alias, an available device, and live Google/TOTP checkout. Operator seed
identities are not Google accounts and must not be substituted for that proof.
