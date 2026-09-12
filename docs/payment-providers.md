# One NoEBS account, multiple payment providers

The app signs into NoEBS through its configured OIDC identity authority. It does
not sign into Mojaloop. The same authenticated customer and tenant own their profile,
wallets, transaction history and identity evidence. Mojaloop is one configured
payment rail; the switch receives only the native transfer data needed for
interoperability. Customer balances remain in NoEBS. A hub participant position
is never exposed as a customer balance or treated as customer funding.

## Account and discovery API

All routes require the existing NoEBS mobile bearer token and active tenant.
The gateway resolves the customer projection, and the wallet service verifies
that identity again. Caller-selected `tenant_id` and `user_id` query parameters
are rejected; account and funding queries never accept an owner identifier.

- `GET /wallet/me` returns `{user_id, tenant_id, wallets: [...]}`. The wallets
  use the existing wallet protobuf, with string-backed `balance`,
  `available_balance`, `balance_money` and `available_balance_money`. No account
  is created by a read. Use `POST /wallet/wallets` with an explicit currency to
  create a wallet. The normal profile/name endpoint remains `GET /consumer/user`.
- `GET /wallet/providers` returns `{providers: [...]}`. Each entry has `id`,
  `name`, `available`, `currencies`, `transfer_mode`, `funding_mode` and
  `capabilities: {send, receive, funding, services}`. The directory aggregates
  tenant-scoped enabled PSP methods and the explicitly configured native rail.
- `GET /wallet/funding-methods?wallet_id=<uuid>` returns `{methods: [...]}` for
  an owned wallet only. A different customer's wallet returns 404. The methods
  are scoped to the wallet's exact currency unit version.

The local `noebs` provider uses `transfer_mode: wallet_p2p` and
`funding_mode: account_transfer`. An active personal wallet can receive through
its opaque wallet reference without a phone alias or an existing balance.
Sending is advertised only when that wallet's exact unit and KYC tier have an
active P2P fee tier and transaction limit with an admissible positive amount.
The provider also requires the configured Temporal runtime. Discovery does not
guarantee current worker health, sufficient balance or remaining period budget.
Wallet creation is an explicit account operation independent of provider
discovery; refresh discovery after opening a wallet.

The local receiving method is `noebs:receive`, mode `account_transfer`, with the
owned wallet UUID in `account_identifier`. The ledger returns no invented name.
After successful ownership validation, the gateway obtains only the canonical
account display name through a signed identity-auth request. The identity
service retains its database authority; the wallet service never reads profiles.

`POST /wallet/p2p/preview` resolves an exact active same-tenant personal recipient
reference and returns the fee and total debit for review. The gateway adds the
canonical recipient name while preserving the sender's signed identity. Submit
the reviewed immutable terms through the existing transaction-authorized
`POST /wallet/p2p`, then restore the owner-scoped outcome with
`GET /wallet/p2p/status?idempotency_key=...`. A temporary name lookup failure must
not hide the persisted payment status. See the
[shared outbound policy proposal](p2p-shared-outbound-policy.md) for activation
without increasing the aggregate outbound allowance.

Example registered native receiving method:

```json
{
  "id": "mojaloop:receive",
  "provider_id": "mojaloop",
  "label": "Bank or wallet transfer",
  "instructions": "From a participating bank or wallet, send to this registered number. Your NoEBS balance updates when the transfer completes.",
  "account_identifier": "249900000088",
  "account_name": "Amina Hassan",
  "currency": "SDG",
  "available": true,
  "mode": "external_transfer",
  "unavailable_reason": "",
  "currency_unit_version": "17",
  "input_schema_json": ""
}
```

The name, number and unit version in this example are illustrative. The live response
reads the operator-registered alias and the wallet's immutable currency unit; it
never derives a receiving number from profile contact data or installs an alias.
An unregistered wallet returns `available: false`, empty receiving details and
`unavailable_reason: registration_required`. Frozen wallets return
`wallet_inactive`; a disabled binding returns `provider_unavailable`. Unsupported
currencies/units have no native receiving method. Mojaloop send/receive
capabilities require the authenticated customer's active personal wallet in the
bound currency unit and its existing registered alias. The enabled participant
binding alone keeps funding setup visible; it does not enable transfers for a
new or unregistered customer. Another customer's alias grants no capability.
Provider availability describes
configuration and admission enablement, not a realtime network health guarantee
or approval of a particular amount.

The receiving name comes from `wallet_ledger.interop_aliases.display_name`.
Native party discovery returns the same field, so the app must not substitute a
different profile name only on its receiving screen. This is account display
metadata, not a verified legal name or an identity-review decision.

When registering an actual customer's receiving alias, an operator must resolve
the existing personal wallet's canonical tenant/user identity and deliberately
choose the receiving name. For a personal account using its profile name, take
`identity_auth.users.fullname` from that exact canonical owner; do not copy a
test-wallet label. Keep fixture names confined to fixture-owned aliases.
Existing intentional/custom receiving names must not be overwritten by a
background profile refresh.

A stale setup label can be corrected without re-registering or reassigning the
alias. Use an explicitly authorized operator transaction that checks the exact
tenant, alias, active personal wallet, canonical owner, immutable currency unit
and expected prior name. Update only `display_name` and append a wallet audit
event recording the actor, source profile revision and old/new names. Abort on a
changed binding or unexpected name. Verify wallet/ledger/hold state and original
quote/transfer records are unchanged, then compare the receiving screen and the
party-discovery response. Historical fixture transfers retain their original
provenance; correcting a name does not change the rail's operational readiness.

## Routes retain their monetary meaning

`transfer_mode: interop_quote` uses the existing `/wallet/interop/quotes` and
`/wallet/interop/transfers` APIs. The distinct `wallet.interop.transfer`
authorization, quote ownership, explicit currency version, durable idempotency,
hold reservation and native outcome reconciliation are unchanged. Funding mode
`external_transfer` means the sender initiates a transfer from a participating
bank or wallet; NoEBS credits only a correlated native committed outcome.
Showing instructions does not move funds or create a deposit intent.

Configured PSPs use `transfer_mode: withdrawal` and `funding_mode: deposit` as
applicable, retaining `/wallet/withdrawals`, `/wallet/deposits` and the established
`/wallet/methods` request-schema and amount/region filtering contract. The new
funding method exposes only the public deposit input schema, never private
provider endpoints, secrets, internal participant clearing wallets or SDK state.
A client must support a returned mode before offering its action.

`services` is currently false: this release does not introduce wallet-funded
bill payment, electricity or airtime adapters. Existing card/EBS services retain
their own capabilities. A completed transfer to NoEBS does not prove that an
arbitrary app service can spend that wallet balance. This remains a separate
provider/service admission extension, rather than business logic in Mojaloop.

## Verification and release

Discovery tests cover authenticated account restoration without wallet creation,
canonical customer ownership, tenant isolation, exact string money/version
serialization, explicit unavailable states, configured PSP aggregation and
unchanged balances across receiving-detail refreshes. Store discovery also runs
under the actual `wallet_ledger_runtime` login. The existing native monetary
regression covers command/runtime authority, concurrent admission, durable holds,
callback recovery, balanced posting and idempotent demo provisioning.

Local verification uses an isolated PostgreSQL 18.6 cluster extracted from Ubuntu
packages under `.state/postgres`, bound only to `127.0.0.1:55432`. It contains
synthetic test databases only; the test harness creates and removes its own
service databases. No production database is used. Test invocation:

```sh
source .state/postgres/test.env
go test ./wallet/grpc ./wallet/handler ./wallet/store ./wallet/interop -count=1
go test -race ./wallet/grpc ./wallet/handler ./wallet/store ./wallet/interop \
  -run 'Test(WalletDiscovery|WalletAccount|FundingDiscovery|ListUserWallets|Interop)' -count=1
```

Provider discovery adds no migration or new monetary write permission. Publish
an immutable source image, retain the receipt and promote all application digest
pins through the existing [release workflow](alpha-image-release.md). Retain the
pinned official SDK and the separate native transport release as documented in
[the deployed participant contract](mojaloop-interop.md).
