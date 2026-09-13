# External interop contract

Noebs is an aggregator with its own customer ledger. Mojaloop is an independently
operated external payment service. Noebs deployment provisions its own services,
databases, and Kubernetes resources; it does not provision a switch, its SDK,
participant onboarding, or another repository.

The optional wallet interop worker consumes the SDK contract below. Its service
configuration must provide every connection setting explicitly:

```yaml
noebs:
  service_role: wallet-worker
  interop_tenant: tenant-example
  interop_fsp_id: example-fsp
  interop_sdk_outbound_url: http://100.100.1.5:4001
  interop_sdk_inbound_url: http://100.100.1.5:4000
  interop_backend_listen_address: 0.0.0.0:4002
  interop_backend_allowed_peers:
    - 100.100.1.5
```

These addresses illustrate the configuration shape; they are not a provisioned
service. Use the endpoints and callback peer addresses supplied by the external
service operator. Both SDK URLs must be HTTP(S) origins without credentials,
paths, queries, fragments, or a trailing slash. HTTPS uses normal server
certificate verification. Private HTTP connections require a trusted private
network such as Tailscale or another VPN.

The callback listener is separate from the worker health endpoint. Its allowed
peers are exact private, loopback, or Tailscale IP addresses, matched against the
TCP socket peer. Forwarded headers do not grant callback authority. NetworkPolicy
and VPN routing must permit only the required SDK and callback paths. If routing
rewrites source addresses, verify the resulting peer identity before configuring
the allowlist. Keep this listener private.

When interop is unused, omit its tenant, FSP, and all transport settings. A
partially specified integration fails startup; it never selects a local SDK.
Tenant binding and participant identity remain explicit application data. An
infra deployment does not create balances, aliases, fees, or participant policy.

## SDK API contract

The outbound API supports Noebs quote and transfer dispatch and status queries.
The inbound API supports replaying a reserved transfer through native
`GET /transfers/{id}`. The SDK sends the following backend callbacks:

- `GET /parties/{type}/{identifier}`
- `POST /quoterequests`
- `POST /transfers`
- `PUT /transfers/{id}`
- `GET /transfers/{id}`

The current application protocol supports MSISDN, SDG with an explicit
minor-unit version, SEND, consumer-payer TRANSFER, and zero fees. It expects the
Noebs protocol envelopes implemented in `wallet/interop`; an arbitrary SDK or
switch endpoint is not interchangeable with this contract. Native hub source
is exactly `Hub`; participant IDs are case-sensitive.

Wallet API amounts are strings containing integer minor units. SDK amounts are
decimal strings without trailing fractional zeroes; timestamps have exactly
three fractional digits. The API boundary gives an incoming quote two minutes
when its optional native expiry is omitted. Persisted responses and expiry win
retries; received prepare terms are never rewritten.

## Ledger authority

Noebs controls admission, holds, recovery, and balanced posting. Monetary
admission requires `wallet.interop.transfer` authorization bound to tenant,
owner, quote, and command key. SQL records original protocol data, leased
dispatch, and authoritative outcome receipts. Runtime credentials admit
commands; worker credentials reconcile them.

Only correlated native COMMITTED or ABORTED outcomes settle an obligation.
Timeout, 404, process restart, and post-submission quote expiry retain the hold.
Committed incoming money goes to suspense if its destination becomes ineligible.
Conflicting terminal evidence is quarantined. Outages spanning prepare expiry
can require operator reconciliation.

The stored `sdk-loopback` authority label is retained for the existing event
schema; callback authority now comes from the explicit private transport
configuration. Customer funds and the external participant position remain
separate accounting authorities.

## Verification

Local protocol and transport checks:

```sh
go test -race ./wallet/interop -count=1
go test ./cli -run TestInteropRuntime -count=1
```

A deployed integration also needs evidence that its supplied SDK endpoints are
reachable, callbacks arrive from the configured peers, protocol terms correlate,
and one terminal outcome produces exactly one balanced journal. Local tests and
a healthy Noebs rollout do not establish that external integration is live.
