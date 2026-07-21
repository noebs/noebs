# PSP money unit provenance

Every persisted PSP or manual-transfer amount must carry the immutable
`currency_unit_versions.id` that defines its minor-unit scale. Currency codes
alone are labels and cannot establish what an integer meant after a scale
transition.

The request boundary chooses the unit exactly once. Deposit and manual-transfer
requests inherit the already-pinned wallet unit. A new withdrawal resolves the
requested payout currency at the boundary and persists that unit on the PSP
transaction; an idempotent retry reuses the persisted unit. Temporal workflows
load these identifiers and pass them through validation, PSP amount provenance,
and system-wallet creation. Activities and stores reject a missing or mismatched
identifier and never resolve a "current" unit during a retry.

PSP transaction amount rows pin the amount currency. Rows with an FX rate also
pin both FX base and quote units. An idempotency replay that changes any unit is
a conflict even if the currency code and integer are unchanged.

## Applied FX provenance

An FX-bearing PSP amount persists the exact positive applied rate as a reduced
fraction, `fx_rate_numerator / fx_rate_denominator`. Each integer is explicit,
strictly below `10^38`, and cannot be inferred by the store. The legacy
`fx_rate NUMERIC(18,8)` column remains a required audit/display projection. It
is rounded once, half away from zero, from the exact fraction; it is not the
source of truth. For example an inverse observation of `3.75` is stored exactly
as `4/15`, with projection `0.26666667`. This avoids pretending that a finite
eight-decimal inverse is exact.

The current PSP projection contract supports only positive exact rates whose
eight-decimal projection fits `NUMERIC(18,8)`. A rate outside that envelope is
rejected with a typed error even if its fraction would fit. Widening or making
the legacy projection optional requires a separate wire, rollback, and
operational review; it must not silently clamp or round into range.

Provenance has three explicit tiers:

1. An external PSP rate stores the applied pair, both unit-version IDs, source
   label, conversion time, exact fraction, and decimal projection. Observation
   and quote IDs remain null.
2. An observation-backed rate additionally stores the observation ID and its
   original base/quote code-and-unit snapshot. The source, availability,
   freshness, orientation, and exact direct or inverse fraction are checked in
   both Go and PostgreSQL.
3. A quote-backed rate additionally stores the quote ID. Tenant, observation,
   conversion time, applied input/output orientation, units, and the amount on
   the corresponding quote side must match exactly. Replay compares every
   provenance field and PSP amount rows are append-only.

Public money quotes currently remain non-executable reference previews. A quote
ID on a PSP amount is audit provenance, not evidence of an accepted or committed
dealing price. If an executable flow is later introduced, execution must consume
the quote's stored input/output amounts and rounding result; independently
recalculating with another rounding mode is not equivalent.

DepositWorkflow currently remains same-currency: provider result currency and
amount must match the intent, and the intent's pinned unit is persisted. The
registered cross-currency deposit resolver is not wired into that workflow; it
must not be enabled until its two distinct conversions each carry a complete,
separately named exact-rate snapshot through Temporal history.

## PSP amount-bound policy redesign

The existing `psp_configs.min_amount` / `max_amount` and override columns are
not safe for a provider that supports multiple currencies: one integer cannot
have several currency scales. They must not be assigned a current currency
implicitly.

Replace them with normalized `psp_amount_policies` rows keyed by tenant,
provider, currency, currency-unit version, direction, and optional region. Each
row contains nullable minimum and maximum minor-unit amounts, validates
`0 < min <= max` when present, and has composite foreign keys to the provider
and `(currency_unit_version_id, currency_code)` catalog identity. Resolution
must require the caller's explicit currency-unit ID and use the same region and
direction precedence as PSP configuration overrides.

A migration may copy a legacy bound only when its currency is unambiguous (an
explicit currency override, or exactly one enabled currency) and a unit
effective at the policy creation time can be proved. It must abort for
multi-currency or timestamp-less ambiguous rows and require a reviewed mapping.
After migration, clear or drop the legacy columns so no path can continue to
interpret them without a unit.

Payment-method responses retain the legacy numeric `min_amount` and
`max_amount` fields for existing clients. Exact clients must use the companion
`min_amount_money` and `max_amount_money` objects: their `minor_units` and
`currency_unit_version_id` values are decimal strings at the frontend JSON
boundary, and the remaining fields describe the pinned currency scale and
rendering. These objects are emitted only for a currency-scoped method lookup;
the returned unit version must match the normalized amount policy exactly.

## Deployment gates

The additive migration intentionally aborts instead of guessing when existing
PSP amount rows lack unit mappings, legacy PSP min/max bounds lack reviewed
policies, or historical rows predate an effective unit definition. Operators
must inventory and reconcile those cases before rollout. Do not change the
catalog seed epoch merely to make a preflight pass.

The withdrawal validation activity result now carries the exact conversion
time used by the persisted amount. Existing in-flight Temporal histories were
created with the older result shape and cannot reconstruct that time safely.
Drain or version those workflows (or reconcile them through a reviewed
operator process) before deploying; lower layers must not substitute replay
time as a default.
