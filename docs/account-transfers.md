# Personal account transfers

Local NoEBS transfers use the existing `wallet.p2p` authorization, Temporal workflow and customer double-entry ledger. They do not use the hub's participant-position balance. Cross-participant payments retain the official Mojaloop route and its same-participant rejection.

An active personal wallet can share its opaque wallet UUID through its owned `/wallet/funding-methods` response (`provider_id: noebs`, `mode: account_transfer`). This is an account reference, not a verified telephone number or email alias. There is no directory search: the payer must supply the exact reference shared by the recipient.

`POST /wallet/p2p/preview` accepts `from_wallet_id`, `to_wallet_id`, integer minor-unit `amount`, and `currency`. It binds the sender to the verified tenant and personal account, resolves an active personal recipient in that tenant, and returns the recipient binding, currency unit, amount, exact fee and total debit. The API gateway obtains the canonical name through a signed identity-auth request; the wallet database does not read identity tables. Preview cannot proceed when the canonical name is unavailable.

The app persists the reviewed request before authorization. Existing `POST /wallet/p2p` accepts `expected_fee_amount` and `expected_currency_unit_version` in addition to its existing fields. These optional fields preserve compatibility with earlier clients; the new app always supplies them. Canonical authorization and the immutable command bind both values. Admission and workflow validation compare the current fee/unit with the reviewed values. Posting checks the actual principal, fee, unit and limit usage against the immutable command before changing balances.

`GET /wallet/p2p/status?idempotency_key=…` is scoped to the original sender and tenant. Monetary values and unit versions are decimal strings; `transaction_id` is a positive decimal ledger ID. Status is:

- `reserved`: the command exists without a recorded workflow run.
- `running`: the run was recorded but no durable outcome is visible.
- `completed`: matching committed ledger entries prove this payment settled.
- `failed`: a durable no-debit fence was recorded.

Fee/unit/funds changes and inactive wallets after preview produce a durable failed receipt for the exact command when rejection is definitive. Failure finalization and posting use the same transaction advisory lock. Whichever wins determines the outcome; a late activity cannot debit after failure, and an audit/workflow error cannot erase a committed payment. Worker finalization failure, stalled or externally terminated workflows remain pending rather than claiming no debit. A status `404`, network failure, or expired login is ambiguous and must retain the saved request and its idempotency key. The app never automatically replays a monetary POST. Optional name-service failure never hides a durable status.

Deploy wallet-ledger migration `005_p2p_receipts.sql` before these services. Runtime and worker may insert/read failure fences but cannot edit or delete them. No monetary policy or balances are created by this migration. Provider sending remains unavailable without actual active P2P fee and sender-tier limit configuration and configured Temporal prerequisites. Receiving does not require the recipient to have sender funds or an MSISDN alias. Shared outbound policy is separately configured and must preserve the intended aggregate limit across local and interop payments.

Validation uses disposable PostgreSQL databases, real runtime/worker roles and `go test -race`: exact recipient/tenant boundaries, authorization digest changes for fees/units, post-preview rejection receipts, inactive recipients, immutable settlement amounts/fees, duplicate replay, concurrent failure/posting, and committed-ledger precedence over workflow failure. Fixtures never create customer accounts or funds in the deployed service.
