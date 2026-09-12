# Local NoEBS transfer admission and shared outbound policy

The 12 September 2026 live audit found no P2P fees or transaction limits in
`tenant-mojaloop`. Its active personal wallets use the `unverified` tier and SDG
unit version 14, with operational and ISO exponents both 2. New personal wallet
creation retains the `unverified` tier; identity-review approval does not change
it automatically.

The existing active `interop_out` policy is the reference for this proposed
activation. `interop_in` has the same numerical limits but remains independent.

| Policy | Existing outbound value | Proposed local/shared value |
| --- | ---: | ---: |
| Per transfer | 100,000 minor / SDG 1,000 | Same local per-transfer ceiling |
| Daily outbound | 1,000,000 minor / SDG 10,000 | One combined P2P + interop-out budget |
| Monthly outbound | 10,000,000 minor / SDG 100,000 | One combined P2P + interop-out budget |
| Local fee | No P2P policy currently | Explicit zero percentage, flat and minimum fee |

The existing interop protocol is zero-fee. The proposed local policy preserves
that behavior. It does not introduce a new allowance: simply copying the old
limits into a separate P2P counter would create two outbound budgets, which this
activation avoids.

Migration 006 creates `shared_outbound_limits` with no rows. A row explicitly
selects tenant, KYC tier and immutable currency unit; its `enabled` field controls
the shared ceiling. Existing per-operation limits still apply. Both the P2P and
interop-out reservation paths lock the same source wallet before admission. The
shared check adds current UTC daily/monthly reserved and consumed usage from
both operations, including rows written before activation. It does not copy,
reset, rename or delete those counters. Incoming transfers, other transaction
types, unconfigured tenants/tiers and unrelated units retain their own policies.
Runtime and worker database logins can read the shared policy but cannot modify
it. Configuration remains an explicit operator action.

## Reviewable activation

[`scripts/configure-shared-p2p-policy.sql`](../scripts/configure-shared-p2p-policy.sql)
runs inside an operator transaction. It requires explicit JSON parameters in
`noebs.p2p_policy` and action `activate` or `rollback` in `noebs.p2p_action`.
The prepared host runner is private at
`.state/account-release/configure-p2p-policy.py`; it requires an immutable
application digest and checks every running wallet-ledger/worker instance has
converged on that image before applying the SQL. It also requires migration 006.
The runner pins the exact audited tenant, unverified tier, SDG unit14 and the
ceilings above. Run it on the authority host using the reviewed source checkout:

```text
python3 configure-p2p-policy.py activate-dry-run sha256:<reviewed-application-digest>
python3 configure-p2p-policy.py activate sha256:<reviewed-application-digest>
```

The dry run rolls back. Activation checks the exact current interop-out policy
against the reviewed values, rejects preexisting local/shared policies, and
locks the tenant's wallets while installing one zero-fee P2P tier, matching
P2P limits and the combined daily/monthly ceiling. A durable audit entry records
the source policy, parameters, operator label and actual database session/role.
The fee row references an explicitly named database-maintenance audit principal;
that row confers no application permissions and does not impersonate an OIDC
customer or administrator. The transaction compares wallet, journal, entry,
hold, reservation and period-usage snapshots before committing. Exact retries
do not create another audit event or policy row.

No live policy was changed while preparing this implementation. Activation is
separate from code deployment and does not create accounts, aliases or money.
The existing interop deployment's funding/settlement provenance remains intact.

## Configuration rollback

The same runner supports `rollback-dry-run` and `rollback`. Rollback disables
only the installed P2P fee tier and P2P transaction limit. It retains the shared
ceiling and all usage because earlier P2P spending must continue to reduce the
remaining external outbound allowance. Already admitted obligations keep their
durable reservation/settlement records. Repeated rollback is idempotent;
reactivation after rollback requires a separately reviewed operation.

Keep shared-enforcement code deployed while either operation has relevant
current-period usage. Removing that enforcement would change the allowance even
if P2P admission has been disabled. Configuration rollback never resets a period
or deletes a financial/audit record.

## Deployment receipt — 12 September 2026

The prepared policy was activated at 07:53:29 UTC after a successful rolled-back
dry run. Source `1ae6c315b4bc4e4c40194baa12a3c6b9ce6aff94` was published by
[release run34680710205](https://github.com/noebs/noebs/actions/runs/34680710205)
and deployed through promotion `befae7ad3a16173647b22d229f242c717359311b`.
The exact application image is
`ghcr.io/noebs/noebs@sha256:58212ab8f1bbbb01ff35f6bfa2b5900a0cd0766c07d08a3552e228241c45c1e9`.
Every ledger/worker instance passed the new-image and no-old-pod guard before
activation. The transaction verified unchanged account, money and usage
snapshots. No account was funded and no customer transfer was submitted.

Both Argo applications reached the promotion and reported Synced/Healthy.
The post-deploy smoke check passed, including wallet migrations005 and006 and
the existing public authentication boundaries. The exact two-Application
OpenTofu plan, registry receipts, rendered image checks, policy transactions
and smoke output are recorded in
[the account-transfer release evidence](https://github.com/noebs/mojaloop/tree/main/evidence/live-walkthrough-20260912/account-transfers).
The two-phone transfer remains a separate user-authorized walkthrough.

## Validation

Actual disposable PostgreSQL tests run under the runtime and worker logins.
They cover a race between rails for the same remaining allowance, preactivation
reserved/consumed usage, exact retry, release, UTC daily/monthly selection,
overflow-safe sums, scope isolation, denied policy mutations and the real
interop arm path failing before a hold is created. The same activation SQL is
tested for dry-run rollback, exact activation retry, unexpected-baseline
rejection, configuration rollback/retry and preservation of existing money and
usage. Identity-name and provider-discovery tests also pass with the race
detector; these checks do not stand in for the two-phone payment walkthrough.
