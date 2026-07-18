# PAN-independent card and transaction identity

Status: approved target architecture; implementation and cutover are mandatory before card-funded alpha features are enabled.

This document supersedes every design statement that uses a PAN, masked PAN, last four digits, PIN, or IPIN as an application identity, ownership proof, authorization selector, durable relationship, or cache identity. It does not prohibit a PAN from crossing the final EBS rail boundary when the rail protocol requires it. That is payment routing data, not product identity.

## Decision

Noebs identities are tenant-scoped user IDs. Enrolled cards have a stable, random public `card_id`. Transactions authorize users through immutable participant rows captured when the transaction is created. A PIN or IPIN is a secret proof used for one rail operation; it is never an identity and is never stored reversibly.

The public card contract is a safe summary:

```json
{
  "card_id": "0f8fad5b-d9cb-469f-a165-70867728950e",
  "name": "Daily card",
  "masked_pan": "****1234",
  "exp_date": "2912",
  "is_main": true,
  "status": "active"
}
```

The existing `ebs_fields.Model.ID`/`cards.id` remains a private database key. It is predictable and incidentally exposed today, so it is not the public card identifier. Public `card_id` values are canonical lowercase UUID strings, generated server-side, stable for one enrollment, scoped by tenant and authenticated user on every operation, and never reused. A `card_id` is not a secret or a substitute for authorization. New CardSummary `masked_pan` is exactly four ASCII asterisks plus four digits (`^\*{4}[0-9]{4}$`, for example `****1234`). It exposes no BIN or original PAN length. Android may render bullets and spacing locally; legacy EBS receipt masks retain their existing display shape and never enter CardSummary.

The following are non-negotiable:

- No authorization, history membership, direction, notification recipient, user lookup, card mutation, main-card selection, cache row, payment token, navigation argument, or UI identity may be decided by PAN, a PAN hash, a mask, BIN, last four digits, expiry, PIN, or IPIN.
- Public card reads never return a full PAN, PAN fingerprint, ciphertext, PIN, IPIN, or internal database ID.
- The app does not persist a full PAN, PIN, IPIN, encrypted PIN block, or reversible derivative.
- Card Vault is the only long-lived owner of encrypted PAN material. EBS Adapter may receive clear PAN and expiry only through an authenticated internal rail-resolution call and only for the lifetime of that request.
- A clear PIN/IPIN exists only in transient client memory. Only the encrypted rail block crosses the public API. Neither clear nor encrypted PIN material is written to application databases, analytics, crash reports, logs, saved state, navigation, or events.
- Missing opaque-card capability fails closed. New clients never fall back to a legacy PAN endpoint.

## Why the current model is unsafe

### Mask collisions are expected, not exceptional

`utils.MaskPAN` and `ebs_fields.EBSResponse.MaskPAN` retain the first six and last four digits and replace the middle with a fixed five asterisks. Cards from one BIN therefore have only 10,000 last-four display buckets. Collision probability becomes material with a modest card population and approaches certainty at product scale. The fixed mask also drops original length information, so different-length PANs can produce the same display value.

Current user history and detail authorization query `transactions.pan`, `sender_pan`, and `receiver_pan` using these display masks. Two unrelated users with colliding masks can therefore see the same transaction. This is an authorization failure, not merely a display bug.

### Full PAN is not a durable owner ID

Even an exact PAN cannot represent a Noebs identity:

- an issuer can reissue or rotate it;
- a closed PAN can later be reused;
- imports and operational mistakes can attach one PAN to multiple users;
- joint or delegated instruments need an explicit relationship, not an assumed owner;
- the current card schema permits the same PAN fingerprint under different users;
- `GetUserByCard` uses `LIMIT 1`, making duplicate ownership arbitrary;
- deletion or reassignment must not move historical transactions to the current card holder;
- a payment to an external PAN does not prove that its holder is a Noebs user.

A deterministic PAN fingerprint is useful for internal duplicate detection. It remains sensitive, key-versioned technical data. It is not a public identifier, foreign key, authorization claim, or history key.

### Rotation and deletion currently rewrite history visibility

History is discovered from the user's *current* card list. Removing a card hides its transactions. Adding a new card with a colliding mask can reveal older unrelated transactions. Changing a card PAN changes which history appears. None of those card-lifecycle actions should change immutable transaction participation.

### The client stores and propagates the wrong identity

The Android `Card` Room entity stores raw `pan`, `ipin`, and `newIpin`; uses card name as its primary key and PAN as a unique index; updates balances and counts by PAN; passes a full PAN through cash-in navigation; uses PAN as Compose list identity; and parcels full card objects through saved state. Card name collisions can also transfer cached balance to the wrong card during `replaceAll`.

The history model is especially misleading: `TransactionParty.Card(val pin: String)` actually contains a PAN/masked PAN, serializes it with the `crd` prefix, and uses it for display. That field is not an actual PIN, but it is still PAN identity disguised as `pin`. It must be migrated, not renamed in place and trusted.

`Contact.mainCardMaskedPan` and the current SDK `IsUser.PAN` make a recipient's mask part of contact identity. The backend now intentionally returns only `phone` and `is_user`; the Android `matchingTransferRecipient` function still requires `candidate.PAN`, so the current phone-transfer checkout always rejects the safe response.

### PIN/IPIN persistence is unnecessary and dangerous

The backend `cards` table and `ebs_fields.Card` can persist reversibly encrypted IPIN and return it through the generic card JSON model. The Android card table reserves clear `ipin` and `newIpin` columns. There is no legitimate identity or business requirement for either.

Wallet PIN handling is different: `wallet_pin_hash` is a one-way bcrypt verifier selected by tenant and wallet ID. No production query uses PIN or its hash as an owner ID, lookup key, or index. That design may remain. EBS PIN/IPIN blocks and configured bill-inquiry credentials are also legitimate rail proofs, provided they remain secret, are never logged, and never become selectors.

## Current touchpoint inventory

This inventory distinguishes forbidden product linkage from legitimate, bounded rail payloads. Tests, generated Swagger, Bruno examples, Room schema snapshots, deployment examples, and runbooks that mirror a changed contract must be updated with the owning implementation; they are not separate compatibility authority.

### Backend: forbidden identity, selector, storage, or ownership linkage

| Area | Current touchpoints | Required disposition |
| --- | --- | --- |
| Transaction authorization | `store.GetTransactionsByMaskedPan`, `store.GetTransactionByUUIDForMaskedPANs`, `consumer.GetTransactionsForUserID`, `consumer.GetTransactionByUUIDForUser`; `transactions.pan`, `sender_pan`, `receiver_pan` indexes in EBS Adapter and Admin Reporting | Replace user reads with tenant + participant user ID. Masks remain display-only in payloads. Drop PAN lookup methods and user-facing ownership indexes after cutover. |
| Transaction recording | `store.insertTransaction`, `CreateTransactionWithEvent`, `consumer.recordTransaction`, transfer/quick-pay mutation callbacks | Atomically persist exact trusted participant role pairs with transaction and outbox. Replay validates payload, event, and exact canonical participant-role set. |
| Card public identity | `ebs_fields.Card.Model.ID`, `Pan`, `CardIdx`; `apigateway.Cards.PAN`; public `get_cards`, `add_card`, `edit_card`, `delete_card`, `cards/set_main` handlers and gateway routes | Introduce public UUID `card_id` and safe summary. All reads/mutations/main selection use authenticated `(tenant_id,user_id,card_id)`. Retire legacy routes. |
| Card database selectors | `cards.pan` overloaded as plaintext/hash, `UNIQUE(tenant_id,user_id,pan)`, PAN index; `UpdateCard`, `DeleteCard`, `SetMainCard`, `CardExists`, `GetUserByCard`, `GetPanByMobile`, `GetDeviceIDsByPan`, `panLookupClause/Args` | Split `pan_fingerprint` from `pan_ciphertext`; use card ID for relationships and mutations. Keep fingerprint only for active-enrollment conflict detection. Remove PAN ownership/device lookups. |
| Duplicate main-card identity | `users.main_card`, `main_card_enc`, `main_expdate`, `User.MainCard`, identity-store hydration/update; `cards.is_main` | Identity Auth must not store card data. Card Vault owns one active main `card_id` per user, enforced under concurrency. |
| Mobile-card linkage | `cards.mobile`, `ListCardsByMobile`, internal `/cards/by-mobile`, `/cards/by-mobile-pan`, `/cards/masked-by-mobile`, `Resolve*ByMobile*` | Mobile maps to user in Identity Auth. Card Vault resolves active card by explicit user ID. Remove denormalized mobile and every mobile+PAN ownership command. |
| Recovery identity | public `/consumer/otp/balance`, `BalanceStep`, `ResolveCardByMobilePAN*` use mobile+PAN to choose the user/card before issuing a recovery credential | Resolve the claimed identity by mobile/recovery session, select a verified enrollment by card ID or server-side main card, and treat valid IPIN/EBS response only as proof. PAN never chooses the identity. |
| Public card registration | public `register_with_card`; `RegisterWithCard`; unauthenticated card registration commands; `isValidCard`; completed-registration card persistence | Normal enrollment is authenticated and challenge-based. EBS-issued-card completion may use a narrowly authorized issuance session. A PAN must never create or select an identity account. |
| Card cache | `cache_cards` keyed by `(tenant_id,pan)`, `CacheCards.GetPk() == "pan"`, `UpsertCacheCard`, `GetCacheCard` | Remove the legacy cache or key an enrollment/verification intent by opaque ID. A versioned fingerprint may enforce internal dedupe but may not act as product identity. |
| Payment tokens | `tokens.to_card`, `to_card_enc`, `Token.ToCard`, `QrData.ToCard`, `ExpandCard` matching first/last four, generate/get/quick-pay services | Store payee user ID and payee `card_id`; public QR/link contains an opaque payment-token ID, not a PAN. Resolve the payee's verified card at execution. Expire legacy available tokens at cutover. |
| Contact discovery | historical `CheckUser` PAN response and Card Vault mask lookup; SDK/app still expect it | Contract is exactly `phone` + `is_user`, authenticated and rate-limited. No card lookup occurs. |
| Transfer recipient | `MobileTransfer` and `CardTransfer` resolve/compare raw PAN; notification data copies PAN; `GetDeviceIDsByPan` | Resolve mobile to recipient user ID, then resolve verified main card by that user ID. Notifications target user ID/mobile/device identity independently of PAN. Mask may appear only in a receipt body/payload. |
| Beneficiaries | generic beneficiary `data` can durably store a P2P PAN | In-app beneficiaries use phone/recipient reference. External-card destinations use a backend `destination_id` plus mask, with protected rail data in the appropriate vault. Do not store full PAN in the generic beneficiary service. |
| Identity JSON | generic `User` contains `main_card` and expiry, although sanitizer currently clears them | Remove the fields and columns. Safety must not depend on every caller remembering a sanitizer. |
| IPIN storage | `cards.ipin`, `cards.ipin_enc`; `Card.IPIN`, encryption/hydration/backfill in `store/sensitive.go` | Drop columns and code. Never persist an IPIN or encrypted IPIN block. |
| Legacy PAN type | unused `apigateway.Cards` and stale Swagger/generated docs | Delete or replace so new code cannot revive the PAN contract. |

`store/migrations/postgres/ebs_adapter/104_transaction_participants.sql` is the correct next EBS Adapter migration after 103. It intentionally does not backfill legacy rows. Card Vault, Identity Auth, Notification Chat, and Beneficiary migrations must each inspect their own migration directory again immediately before choosing their next unused number.

### Backend: legitimate rail/display use, with boundaries

| Use | Why it may remain | Boundary |
| --- | --- | --- |
| `ebs_fields` request PAN/to-card/last-four fields and EBS client mapping | EBS requires PAN routing data | Construct inside EBS Adapter from an authenticated card resolution or explicit external destination. Never use response PAN for app authorization. |
| `EBSResponse.MaskPAN`, transaction payload and reporting projection | Receipts/support may display a mask | Mask before persistence/event publication. A mask is non-unique display metadata; reports search by transaction UUID/reference, not ownership. |
| Bill-inquiry PAN/PIN/IPIN/expiry config in CLI, Secret, and deployment validation | EBS service credential for a configured inquiry | Secret-mounted, explicit, redacted, never returned, never a tenant/user/card ID. |
| One-time enrollment PAN and expiry | The rail must verify the instrument being enrolled | Accept only on a short-lived authenticated enrollment intent, do not log, encrypt/fingerprint in Card Vault after proof, zero transient buffers where practical. |
| Explicit external destination PAN | A card-to-card rail may require a destination outside Noebs | Treat only as routing data. Do not infer a Noebs recipient, participant, notification target, or beneficiary owner from it. Prefer a vault-issued destination token for repeat use. |

The Admin Reporting projection can retain masked PAN fields for display. It must not receive card ownership tables or answer consumer authorization queries.

### SDK: forbidden product contract

| File/model or methods | Current dependency | Target |
| --- | --- | --- |
| `data/Card.kt` | `Card.PAN`, `cardIndex`, `UserCards.pan`, `SetMainCardRequest.PAN`; no public card ID | `CardSummary` + `CardRef(cardId)` with canonical UUID validation; safe summary fields only. |
| `TutiApiClient.fillRequestFields` and all card-funded helpers | Copy `Card.PAN` and expiry into every EBS request | Accept `CardRef`/`CardRailAuthorization`; serialize card ID, rail UUID, and encrypted IPIN block. Backend injects PAN/expiry. |
| Card CRUD and list helpers | Legacy PAN request/selector paths | New REST paths and bodies keyed by card ID. No fallback to legacy endpoints. |
| `IsUser` | Nullable `PAN` is part of discovery | Remove PAN; parse `phone,is_user` only. |
| `PaymentToken`, `PaymentRequest` | `toCard`/`cardTobePaid` is embedded and treated as payee identity | Opaque token UUID plus payee display metadata; sender uses card ID. |
| `Ipin`/`IpinCompletion` and `changeIPIN` | PAN/expiry carried from owned-card model | Card ID selects the verified enrollment; IPIN blocks remain UUID-bound transient proofs. |
| `getUserCard(mobile)`/`UserCards` | Fetches another user's card identity by mobile | Remove. Contact discovery and transfer do not expose recipient cards. |
| Entertainment request | Serializes owned-card PAN | Use the same card-reference authorization contract or gate until supported. |
| `EBSRequest.DEFAULT_PUBLIC_KEY` | Silently falls back while encrypting IPIN | Remove the fallback; a missing/invalid configured EBS key fails before accepting PIN input or sending a request. |

Raw EBS DTOs may retain PAN fields in an internal compatibility namespace while the backend adapter still speaks EBS. They must not remain the SDK's public owned-card model. `EBSResponse.pan`, `toCard`, and `LastTransactions.pan` are receipt display fields only and must already be masked when received.

### Android: forbidden identity, persistence, and navigation linkage

| Area | Current touchpoints | Target |
| --- | --- | --- |
| Room card entity | `model/Card.kt`, `CardDao`, Room schemas 1-15: name PK, unique raw PAN, IPIN/newIPIN columns, balance/count selectors by PAN | Account-scoped CardSummary cache keyed by `card_id`; no raw PAN/PIN columns; DAO selectors and balance updates by account + card ID. |
| Card synchronization | `helpers.toTutiCard/toNoebs`, `replaceAll` preserves balance by name | Safe summary mapping; preserve local state by card ID. Duplicate names and masks are valid. |
| Card UI/management | card dialog, card list Compose key, card picker/dropdown, `CreditCard`, edit/delete/main flows | Display `masked_pan`; key/select/mutate by card ID. PAN is entered only in enrollment UI and is cleared when the request completes or screen leaves. PAN is immutable after enrollment. |
| Checkout/home/bills/entertainment | selected `Card.pan` drives balance, bill payment, transfer, voucher, entertainment and IPIN management | Send card ID authorization. Update local balance by card ID. Gate the entire action when server capability is absent. |
| Contact transfer | `TransferCheckout`, `matchingTransferRecipient`, `Contact.mainCardMaskedPan`, receiver-card checkout row | Match exact normalized phone + `is_user`; remove recipient PAN state/display; backend resolves recipient. |
| Cash-in/payment token | PAN in navigation route, SavedStateHandle, repository and QR `cardTobePaid` | Pass card ID as a typed navigation argument; payment link/QR contains opaque token only. Never place PAN in routes, intents, bundles or logs. |
| External card beneficiary | `CardTransferBeneficiary.cardPANNumber` persisted in Room/backend and searchable | Use phone/recipient reference for users. Use backend `destination_id` + mask for external cards; otherwise disable saving the beneficiary. |
| History ownership/direction | `TransactionRecordConverter` builds parties from response PAN; `TransactionParty.Card.pin`; Room converter persists `crd<pan>`; UI displays it | Server membership drives access. Persist a neutral/viewer-role DTO keyed by transaction UUID. A mask is optional display text only. Purge legacy party serialization. |
| Result screens | `ProcessResultToFields` and transaction components display response PAN | May render a server-masked display value; may not compare it or use it as a key. |
| Saved state/account switching | Parcelable Card carries raw PAN; one global database can show stale prior-account cache after failed refresh | Store no raw card data and namespace caches by a stable authenticated account subject. Session change switches/purges scope atomically before showing home. |

The explicit Android migration must remove `Card.pan`, `Card.ipin`, `Card.newIpin`, `Contact.mainCardMaskedPan`, full-PAN beneficiary data, and legacy serialized `TransactionParty.Card`. Do not preserve those values in a temporary table. Card and history caches are re-fetchable; purge unsafe legacy cache rather than guessing mappings. Do not rely on generic destructive fallback that unnecessarily erases unrelated chat and beneficiary data.

### Concrete source inventory

The semantic inventory above maps to these production sources. This is the removal/checklist surface; a search for case variants of `pan`, `toCard`, `last4`, `card_index`, `main_card`, `pin`, and `ipin` must be rerun before final deletion because generated contracts and tests will move with implementation.

Backend persistence and ownership:

- `store/migrations/postgres/identity_auth/101_identity_auth.sql`: identity-owned `main_card`, ciphertext and expiry.
- `store/migrations/postgres/card_vault/101_card_vault.sql`, `103_card_mobile.sql`, and `104_payment_token_state.sql`: PAN-keyed cards/cache, denormalized mobile, IPIN storage, and PAN-backed payment tokens.
- `store/migrations/postgres/ebs_adapter/101_ebs_adapter.sql` and `admin_reporting/101_admin_reporting.sql`: PAN display columns and lookup indexes on transaction projections.
- `store/store.go`, `store/sensitive.go`, `store/crypto.go`, `store/errors.go`: PAN/hash dual lookups, CRUD/main selectors, cache keys, token destinations, transaction lookup, device/user lookup, reversible IPIN hydration and legacy plaintext backfill.
- `store/transaction_events.go`, `store/transaction_projection.go`, `internal/eventing/events.go`: masked receipt/event projection; safe only when participant authorization is independent.
- `store/migrations/postgres/ebs_adapter/104_transaction_participants.sql` and `store/transaction_participants.go`: replacement membership path; must retain tenant and exact roles.

Backend public/internal service surface:

- `ebs_fields/users.go`: `User.MainCard`, `Token.ToCard`, `QrData.ToCard`, `Card.Pan/IPIN/CardIdx`, cache-card PAN PK, and masked-selector `ExpandCard`.
- `ebs_fields/fields.go`, `ebs_client.go`, and `consumer/types.go`: EBS protocol PAN/last-four/PIN fields; retain only as bounded rail DTO/display fields.
- `consumer/handler/routes.go`, `user.go`, `misc.go`, `ebs.go`, `payment_tokens.go`, `card_lookup_internal.go`, and `card_registration_internal.go`: legacy public PAN CRUD, recovery, transfers, token paths and internal PAN lookup commands.
- `cli/gateway_proxy.go`: gateway registrations for all legacy card/PAN routes and the future disable/410 boundary.
- `consumer/user_service.go` and `user_misc_service.go`: card CRUD/main and former masked-PAN history authorization.
- `consumer/card_lookup_commands.go`: mobile -> PAN, mobile+PAN -> user, and masked-card projections.
- `consumer/card_registration_commands.go`, `registration_service.go`, `services.go`, and `balance_step_service.go`: PAN-bearing registration, validation, cache persistence and recovery identity.
- `consumer/payment_tokens_service.go` and `quick_pay_commands.go`: PAN destination token generation, masked selector expansion and quick-pay rail resolution.
- `consumer/transfer_service.go`, `bill_service.go`, `voucher_service.go`, `ebs_extra.go`, `ebs_proxy.go`, and `service.go`: rail PAN injection, receipt display, recipient linkage, notification payload and transaction recording.
- `consumer/identity_user_commands.go`: correct mobile -> user resolution that must precede Card Vault user-ID resolution.
- `consumer/notification_commands.go` plus Notification Chat `push_data`: preserve user/mobile/device targeting; remove any future PAN routing and delete dead `GetDeviceIDsByPan`.
- `apigateway/fields.go`: unused PAN-centric `Cards` compatibility type.
- `adminreporting/service.go` and `dashboard/*`: masked reporting display only; never a consumer ownership source.
- `wallet/security/pin.go`, `wallet/activity/security.go`, `wallet/store/wallet.go`, and wallet schema: accepted one-way wallet PIN proof, included in regression search to ensure it never becomes a selector.

Backend ancillary contract sources to update with their owning change include `docs/microservices-architecture.md`, generated `docs/docs.go`, `swagger.json`, `swagger.yaml`, Bruno requests under `Noebs -- Merchant APIs`, deployment secret examples, `scripts/alpha-http-e2e.sh`, and PAN/IPIN-focused tests. Their rail examples may retain clearly synthetic routing values, but ownership expectations must use IDs.

SDK:

- `lib/src/main/java/com/tuti/api/data/Card.kt`: owned-card model, CRUD selector, signup card, main-card response/request.
- `data/IsUser.kt`: stale recipient PAN.
- `data/PaymentToken.kt`: `toCard` payment/payee identity and QR parsing.
- `data/Beneficiary.kt`: PAN-bearing IPIN setup values; transport only after card-reference migration.
- `data/entertainment.kt`: owned PAN copied into an external transfer request.
- `api/ebs/EBSRequest.kt`: rail PAN/toCard/last-four and encrypted IPIN block, plus forbidden default-key fallback.
- `api/ebs/EBSResponse.kt` and `LastTransactions.kt`: display-only response masks.
- `api/TutiApiClient.kt`: `fillRequestFields`; signup/card CRUD/list/main; balance; every biller; voucher; card/mobile transfer; payment-token/quick-pay; IPIN generation/change; and obsolete `getUserCard(mobile)`.
- `model/Operations.kt`: legacy endpoint names that must not be selected by the new capability path.
- `util/IPINBlockGenerator.kt`: UUID-bound proof generation; keep transient and fail without an explicit key.

Android card persistence and management:

- `app/src/main/java/com/tutipay/app/model/Card.kt`, `utils/CardDao.kt`, `utils/AppDatabase.kt`, `di/AppModule.kt`, `utils/helpers.kt`, and all Room schema snapshots: raw card/IPIN storage, name/PAN keys and SDK mapping.
- `feature_card_dialog/ViewModel.kt`, `CardsScreen.kt`, `AddOrEditCardScreen.kt`: add/edit/delete/list identities and PAN-bearing saved card arguments.
- `core/ui/components/CreditCard.kt`, `core/ui/components/input/CardPicker.kt`, `CardPickerDropDown.kt`, reusable card-number fields, `core/ui/utils.kt`, `core/validation/Matchers.kt`, and `utils/Globals.kt`: enrollment input versus display masking.

Android funded flows and navigation:

- `checkout/ui/CheckoutViewModel.kt`; `feature_checkout/BillPaymentCheckout.kt`, `TransferCheckout.kt`, and `PaymentTokenCheckout.kt`: selected owned card, balance update, recipient PAN and transfer execution.
- `feature_home/ui/HomeViewModel.kt`: card sync/selection and balance by PAN.
- `feature_billpayment/ViewModel.kt`, `PerformTransaction.kt`, `PurchaseRequest.kt`, and related screen state/UI: owned PAN and durable external-card beneficiary routing.
- `feature_transfer/ViewModel.kt`: payment-token destination selected by PAN.
- `feature_cash_in/data/CashRepository.kt`, `ui/input/CashInScreen.kt`, `ui/navigation/CashInGraph.kt`, and `ui/qr/CashInQrViewModel.kt`: full PAN in repository, route and QR token.
- `feature_cash_out/ui/sendMoney/SendMoneyViewModel.kt`: PAN copied into quick-pay request and token destination treated as identity.
- `feature_entertainment/ui/checkout/EntertainmnetCheckoutViewModel.kt`: owned PAN in entertainment transfer.
- `feature_ipin_management/generate_ipin/GenerateIpinFlowViewModel.kt` and OTP flow: migrate owned card selection to card ID while keeping clear IPIN only in transient screen state.

Android recipient/history linkage:

- `model/Contact.kt` and `utils/ContactDao.kt`: persisted recipient masked PAN.
- `model/Beneficiary.kt`, beneficiary/bill-payment view models and cards: persisted/searchable external PAN.
- `feature_history/data/TransactionsRepository/util/TransactionRecordConverter.kt`; `transactionRecord_localDataSource/model/TransactionParty.kt`, `Converters.kt`, `TransactionRecord.kt`; and history/detail UI: PAN-derived party, misnamed `pin`, serialized linkage and direction.
- `feature_result/ProcessResultToFields.kt` and result UI: permitted only as server-masked receipt display.
- Android navigation/instrumentation tests and `TransferRecipientTest`: update to assert card IDs and PAN-free recipient discovery rather than preserve the legacy contract.

## Target backend model

### Verified card enrollment

Card Vault owns a record shaped around an enrollment, not a PAN identity:

```text
cards
  id                 bigint private primary key
  tenant_id          text not null
  card_id            uuid not null
  user_id            bigint not null
  pan_fingerprint    text not null
  pan_ciphertext     text not null
  pan_key_version    integer not null
  masked_pan         text not null
  expiry             text
  name               text
  status             active | legacy_unverified | retired | blocked
  is_main            boolean not null
  verification_method text
  verified_at        timestamptz
  retired_at         timestamptz
  created_at/updated_at
```

Required constraints are:

- unique `(tenant_id, card_id)`;
- one active enrollment for `(tenant_id, pan_fingerprint)` unless a reviewed joint-card model is introduced;
- at most one active main card for `(tenant_id,user_id)`;
- `verified_at` is present before status can be `active`;
- retired card IDs and transaction participants are never reassigned.

Use separately named, versioned HMAC fingerprint and authenticated ciphertext fields. The current `pan` column's plaintext-or-hash overloading and dual lookup fallback must not survive cutover. The canonical last-four-only CardSummary mask is generated from the verified clear PAN once and stored only for display; legacy first-six receipt masks remain isolated in EBS transaction DTOs.

Any PAN change creates a new enrollment and new `card_id`; the old enrollment is retired. An expiry-only refresh may keep the card ID only after fresh rail verification of the same fingerprint. A retired fingerprint can later be enrolled by another verified user if issuer reuse is proven; this creates a new card ID and never transfers old history. After retention needs end, cryptoshred retired PAN ciphertext while retaining card ID and safe mask for audit.

Enrollment is a short two-step protocol:

1. An authenticated user creates a short-lived enrollment intent. The server returns `enrollment_id`, a rail UUID, expiry, and the explicitly configured EBS public key metadata.
2. The user submits PAN, expiry, name, and an encrypted IPIN block bound to that rail UUID. EBS Adapter validates the card through a non-mutating EBS operation. On success it idempotently commands Card Vault to create the enrollment for the authenticated user.

The intent is single-use and rate-limited. Failure, timeout, invalid IPIN, replay, user mismatch, or ambiguous prior enrollment creates no active card. Clear IPIN is never sent; encrypted IPIN is never stored. Clear PAN is not put in an event or log. EBS-issued card completion uses a narrowly authorized issuance intent and records its verification origin; it does not use PAN to create/select the user.

### Rail resolution

Every owned-card-funded request carries a typed authorization value, conceptually:

```json
{
  "card_authorization": {
    "card_id": "0f8fad5b-d9cb-469f-a165-70867728950e",
    "rail_uuid": "be4fcb08-0e91-4aa8-8657-1c9547c3dbd3",
    "ipin_block": "base64-ciphertext"
  }
}
```

The API boundary validates UUIDs and required fields. EBS Adapter sends an exact-service-authenticated internal command to Card Vault with explicit tenant ID, authenticated user ID, card ID, rail purpose, and rail UUID. Card Vault rejects ownership mismatch, non-active status, missing verification, or disallowed purpose and returns clear PAN/expiry only to EBS Adapter. The response is request-scoped, is never cached, and is excluded from structured logging and traces.

For mobile transfer, EBS Adapter resolves the normalized recipient phone to a tenant user ID through Identity Auth, then asks Card Vault for that user's verified main card. The app and Identity Auth never see the recipient PAN. If there is no active main card, return a typed unavailable-recipient error without disclosing card details.

An external destination PAN may be supplied only on an explicitly named external-card rail field. It never creates a recipient participant or notification target. Repeat external destinations become a protected vault `destination_id`; generic beneficiary data stores only that ID and a mask.

### Transaction participants

EBS Adapter migration 104 adds tenant-scoped transaction participants beside `transactions` and the outbox. Existing transactions are deliberately not backfilled because PAN masks cannot prove ownership.

The implementation stores exact role-bearing facts:

```text
transaction_participants
  transaction_id
  tenant_id
  user_id
  role              actor | recipient
  created_at
  primary key (transaction_id, user_id, role)
  index (tenant_id, user_id, transaction_id)
```

The authenticated gateway user is the actor for consumer operations. A mobile-transfer recipient comes from the verified mobile -> user -> card resolution. A quick-payment recipient is the claimed token owner. An external raw PAN alone never produces a recipient row. Merchant-only and public bootstrap rail calls have an explicit no-consumer-participants mode; absence of participant context is an error, not a silent default.

Transaction, participant roles, and outbox event are inserted in one database transaction. Idempotent UUID replay compares:

- canonical transaction payload equality;
- event identity and payload equality;
- exact canonical `(user_id,role)` set equality.

A subset, superset, changed role, invalid ID, or duplicate pair rejects the replay. List/detail authorization uses tenant plus participant membership and returns a transaction once even if the viewer has actor and recipient roles. Legacy rows without participants are invisible to consumer history/detail and remain available only through audited support/admin reporting.

Participant roles are durable facts, but they do not by themselves define debit/credit for every EBS operation. The legacy history response remains neutral. A versioned transaction DTO must return `viewer_roles` and, only where the domain operation proves it, a server-derived `direction`. Android must never infer incoming/outgoing by comparing PAN masks.

### Notifications and payment tokens

Notification Chat should store/target `(tenant_id,recipient_user_id)` or a verified session/device identity. Phone may remain delivery metadata during the service split. PAN does not select a device, chat, notification row, or callback.

Payment tokens store the authenticated payee user ID and payee card ID. Public QR/deep links carry only a versioned opaque token ID (and tenant routing only if required). Retrieval can return amount, note, payee display name, and optional masked destination display; it never returns a resolvable destination PAN. Claiming records payer user ID; execution resolves both card IDs at the rail boundary.

## Public API cutover

The clean card surface is:

```text
GET    /consumer/cards
POST   /consumer/cards/enrollment-intents
POST   /consumer/cards/enrollment-intents/{enrollment_id}/confirm
PATCH  /consumer/cards/{card_id}             # name/display metadata only
DELETE /consumer/cards/{card_id}             # retire
PUT    /consumer/cards/{card_id}/main
```

All paths are authenticated except narrowly scoped issuance bootstrap. A card belonging to another tenant/user returns the same not-found contract as an unknown card ID. PAN is immutable through PATCH.

Contact discovery remains:

```json
[{"phone":"0912345678","is_user":true}]
```

Mobile transfer accepts sender card authorization and normalized recipient phone (or a short-lived opaque recipient reference). Identity Auth resolves the phone to user ID, Card Vault resolves that user's verified main card, and transaction recording persists both roles.

Capability discovery must expose a versioned opaque-card capability, for example `card_reference_version: 1`. A new SDK or Android client calls only the new endpoints when that capability is present. If it is absent, card management and every card-funded action show a clear unavailable/upgrade state and make no legacy request.

Legacy PAN endpoints may coexist server-side for a short old-client migration window, but:

- the alpha tenant disables them as soon as the new client is installed;
- new clients never call them or retry through them;
- access is measured and alerts identify remaining old clients;
- the gateway returns `410 upgrade_required` after the bounded window;
- compatibility code is then deleted rather than retained as a second architecture.

## Data migration and cutover rules

### Backend expansion

1. Add `card_id`, explicit fingerprint/ciphertext fields, mask, status, verification provenance, and constraints in Card Vault using the next unused migration number at implementation time.
2. Generate random card IDs for existing rows. Existing `user_id` relationships may be preserved because they are explicit stored relationships; do not derive a user from PAN/mobile/mask.
3. Mark every legacy card `legacy_unverified` unless durable verification provenance already exists. The current schema does not record enough provenance, so a successful historical transaction or `is_valid` flag must not be guessed into proof.
4. Quarantine unreadable ciphertext and all active duplicate-fingerprint rows. Do not choose the first user, newest row, main row, or most active row. Require re-enrollment/support remediation with non-enumerating errors.
5. Preserve an existing main selection only within the same stored user relationship and only if it is unambiguous. It cannot fund until verified. Multiple/zero main rows require user selection after verification.
6. Add new API/internal rail paths and participant-backed history. Keep masks display-only.

### Legacy financial state

- Do not backfill transaction participants from full PAN, hash, mask, current card ownership, notification phone, or last four. Legacy consumer history is unavailable until an independently verifiable source exists.
- Invalidate/expire legacy *available* payment tokens that contain `to_card`; owners generate new tokens after card verification. Paid/final tokens remain audit records and are not remapped by guessing.
- Do not move old notifications or chats between users based on PAN. Dead `GetDeviceIDsByPan` behavior is deleted.
- Reissue/rotation always creates a new card ID. Deletion/retirement does not delete participant history.

### Client migration

1. Publish additive SDK `CardSummary`, `CardRef`, card-authorization and new endpoint models. Remove PAN from `IsUser`. Do not overload legacy `Card` so a compiler can accidentally serialize a mask into a PAN field.
2. Add an explicit Room migration (expected database version 16, after verifying the current schema) that creates an account-scoped safe card table, drops unsafe card rows/columns without copying secrets, removes contact PAN linkage, removes full-PAN beneficiary rows, and clears/refetches legacy PAN-based transaction cache.
3. Replace every DAO, UI key, selection, balance update, SavedState/navigation value and network request with card ID. Duplicate names/masks must work.
4. Namespace local caches by a stable authenticated account subject, not merely a recycled mobile number. Switch/purge scope before home renders after login/account change.
5. Ship the new client with legacy funded/card paths fail-closed. Enable them only after the server capability and fixture acceptance suite pass.
6. Disable legacy gateway routes for the alpha tenant, monitor, then remove legacy backend/SDK/App code and sensitive columns.

There is no dual-read rule that guesses `card_id` from a mask. There is no app migration that turns a PAN into an ID. There is no compatibility fallback that sends a masked PAN into a field named PAN.

## Acceptance tests

Unit tests are necessary but not sufficient. The release gate uses real Postgres migrations, real HTTP service boundaries, an isolated EBS/SMS fixture, MockWebServer SDK contracts, and an installed Android build.

### Backend database and API tests

1. Create users A and B in the same tenant with verified cards whose first six and last four digits are identical but middle digits differ. Record a transaction with A as participant. B's list is empty and B receives 404 for A's UUID. Repeat across tenants with colliding numeric user IDs.
2. Delete A's card, enroll a replacement, change main card, and re-fetch. A retains participant history; B never gains it. Re-enroll a retired issuer-reused PAN to a different verified user and prove old history does not move.
3. Replay the same transaction UUID/payload/event with participant pairs in a different order: it succeeds idempotently. Replay with a missing/extra user, changed actor/recipient role, duplicate pair, or invalid ID: it fails and leaves transaction/event/participants unchanged.
4. Prove a self-transfer with actor and recipient roles appears once in list/detail and returns both viewer roles. Prove an unmarked consumer recording call fails and writes no transaction/outbox row.
5. Insert a pre-104 legacy transaction with a colliding mask and no participants. It is absent from both users' history/detail but remains available through an authorized admin/reporting lookup.
6. Assert every card list/mutation response recursively lacks `pan`, `PAN`, `ipin`, `pin`, ciphertext, fingerprint, and private ID fields. Duplicate card names and duplicate masks remain distinct by card ID.
7. Attempt list/edit/delete/main/rail resolution with another user's valid card ID, another tenant's card ID, a random UUID, and a retired ID. All public ownership failures are non-enumerating and no state changes.
8. Race main-card selection and enrollment. Database constraints leave exactly one active main and at most one active fingerprint. Duplicate active enrollment returns a generic conflict without revealing the other user.
9. Exercise enrollment success, invalid IPIN, wrong rail UUID, expired intent, replay, user/session mismatch, EBS timeout, Card Vault failure/retry, and unreadable key material. Only one verified card is created on success; no PIN block or clear PAN appears in DB/event/log output.
10. Exercise mobile transfer with two colliding masks. Recipient participant and notification use the user resolved from the exact normalized phone; no recipient card field is returned to the caller. A user with no verified main card gets a typed unavailable response.
11. Generate/claim/pay a new payment token. Database and public token contain payee user/card IDs or opaque token ID, never PAN. Claim replay and participant mismatch fail. All legacy available PAN tokens are rejected after cutover.
12. Verify external destination PAN can reach the controlled EBS request but creates no recipient user participant or PAN-based notification. Stored receipts/events contain only a mask.
13. Inspect final schemas: Identity Auth has no main PAN/expiry; Card Vault has no IPIN columns; notification/beneficiary stores have no PAN ownership fields; no consumer history query references PAN columns.

### SDK contract tests

1. Decode `check_user` with only `phone,is_user`; selection accepts exactly one normalized matching user and never requires PAN.
2. Reject malformed/non-canonical card IDs before network I/O. Encode list/edit/delete/main/funded requests and assert path/body contain card ID but no owned PAN/expiry.
3. Verify all card-funded helpers bind the encrypted IPIN block to the same rail UUID sent in the request. Clear IPIN is absent from serialized bodies and captured logs.
4. Remove configured EBS key and assert a typed failure before PIN entry/serialization; no default public key is used.
5. Present an opaque-card-capability-missing response and assert no legacy endpoint is attempted.
6. Parse history with duplicate masks and viewer roles without comparing mask values or computing ownership locally.

### Android unit and instrumented tests

1. Upgrade a version-15 database populated with raw PAN, non-empty IPIN/newIPIN, contact mask, PAN beneficiary, and `crd<pan>` history. Verify none survive the explicit migration; unrelated safe chat data remains.
2. Cache two cards with the same name or mask and distinct card IDs. Both render and select correctly; edit/delete/main/balance update affects only the selected ID.
3. Log in as user A, populate cards/history, then switch to B while network refresh fails. No A card, balance, recipient mask, or history renders in B's scope.
4. Inspect Compose semantics and navigation/SavedState bundles for add/edit/cash-in/payment flows. No full PAN is present after enrollment submission; routes carry card ID.
5. Return `[{phone,is_user:true}]` from the real SDK mock and complete recipient checkout without receiver PAN. Zero, false, duplicate, or normalized-phone mismatch fails cleanly.
6. Disable opaque-card capability and tap every card management/funded menu. Each shows the secure unavailable state and the network recorder observes zero legacy PAN endpoint calls.
7. Render transaction history with colliding masks, actor/recipient/self roles, card deletion and reissue. Direction is server-provided or neutral; no PAN inference occurs.

### Actual isolated end-to-end device journey

Run this on the disposable alpha tenant and isolated EBS/SMS capture fixture, never with real funds or personal cards:

1. Install a clean signed candidate and create two actual app users through signup, captured OTP, signature verification and login.
2. Enroll controlled cards A and B engineered to share BIN + last four but differ in the middle. Confirm the app stores only card IDs/masks and the backend marks both verified for their correct users.
3. Exercise list, duplicate display name, edit name, select main, balance inquiry fixture, bill fixture, payment-token creation/claim, mobile recipient discovery, transfer execution and transaction detail from menus—not direct API calls alone.
4. Confirm sender/recipient histories and viewer roles from both accounts. Try each other's card ID and transaction UUID through the API and app deep links; access fails without leakage.
5. Retire A, re-enroll a replacement, and repeat history. Simulate issuer PAN reuse for a third fixture user; historical ownership remains unchanged.
6. Force offline/server-capability-missing states, account switch, process death and reinstall/DB upgrade. No raw PAN/PIN appears in Room, SavedState, logcat, analytics, crash capture, request logs, Kafka events or notification data.
7. Tear down fixture users, enrollment intents, tokens, APK data and temporary routes. Preserve only redacted pass/fail evidence keyed by transaction/card IDs.

## Release gate

Card management and card-funded alpha functionality is releasable only when:

- transaction participant migration and exact replay tests pass on real Postgres;
- opaque card APIs and verified enrollment are deployed from the same immutable candidate;
- legacy PAN routes are disabled for the alpha tenant;
- the SDK and Android app use card ID with no fallback;
- unsafe backend and Room PIN/IPIN persistence is removed;
- the collision, reissue, deletion, payment-token, account-switch and actual device journeys above pass;
- database, event, API, logcat and server-log evidence contains no clear PAN or PIN outside the controlled in-memory rail request.

Until then, the client must keep the affected menus fail-closed. A safe unavailable state is an alpha limitation; silently retaining PAN identity is not.
