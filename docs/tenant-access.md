# Tenant access management

An identity can hold several independent roles in one tenant. The known roles are `user`, `backoffice` and `tenant-admin`. There is no implicit inheritance: granting `tenant-admin` preserves consumer wallet access only if `user` is also granted. The existing financial approval and maker/checker rules remain separate from role assignment.

The authority flow is shared by browser and API clients:

```mermaid
flowchart LR
  A[Hosted Keycloak login] --> B[Frontend selects tenant]
  B --> C[Validated JWT roles and action permissions]
  C --> D[Gateway session and tenant policy]
  D --> E[Signed internal principal and one action permission]
  E --> F[Identity-auth access service]
  F --> G[Committed SQL intent and enrollment suppression]
  G --> H[Keycloak explicit role delta]
  H --> I[Verified result and immutable audit]
```

Signup uses the same identity lock and pending journal boundary, then the ordinary
per-tenant profile/KYC/wallet flow. Operator access does not require a consumer
profile. The Android and website frontend select `noebs`; headers alone confer no
membership or permission.

| Role | Managed action permissions | Consumer access |
| --- | --- | --- |
| `user` | None of the operator action permissions | Existing user/profile/wallet routes |
| `backoffice` | `reporting:read`, `wallet:read`, `wallet:audit:read`, `identity:review:read` | Only when `user` is separately granted |
| `tenant-admin` | The four read permissions above, plus `wallet:manual:create`, `wallet:fees:write`, `wallet:rates:write`, `wallet:workflow:approve`, `wallet:workflow:reject`, `identity:review:decide`, `wallet:transaction:resolve`, `identity:access:read`, `identity:access:write` | Only when `user` is separately granted |

The bundles compose by set union. Authorization still checks both the actual role
and actual permission in the token; a role name is never substituted for a missing
permission. Keycloak reconciliation owns exact noncomposite group mappings, and
the access adapter rejects mapping drift before changing membership.

The private backoffice provides the operator workflow:

1. Sign in at the configured private backoffice origin through the shared Keycloak browser flow.
2. Open **Tenant access** for an authorized tenant. The list contains only current members of that tenant.
3. Inspect a current member, or submit a known exact identity subject. Nonmember inspection does not expose a name or email address and cannot search realm users.
4. Review roles, effective permissions, enrollment suppression and access-change history.
5. Open **Change access**, select explicit grants/revocations and enter a reason. The form carries an operation ID and the revision returned by the authority.
6. If a change is pending, resume its original operation and terms. A revision conflict requires reviewing current access; an uncertain result must be retried with the original operation ID.

Private network admission does not grant application privileges. Both the gateway and identity-auth handler require `tenant-admin` in the selected tenant and the exact action permission. The gateway replaces all caller identity headers with the verified session principal, signs the workload request, validates CSRF/origin on POSTs and strips public credentials before proxying. Identity-auth obtains the actor and tenant exclusively from that signed principal.

| Private backoffice path | Method | Permission | Purpose |
| --- | --- | --- | --- |
| `/backoffice/t/:tenant/access` | GET | `identity:access:read` | Current tenant members |
| `/backoffice/t/:tenant/access/lookup` | POST | `identity:access:read` | Validate an exact subject and redirect, without querying the realm |
| `/backoffice/t/:tenant/access/:subject` | GET | `identity:access:read` | Member access and audit history |
| `/backoffice/t/:tenant/access/:subject/edit` | GET | `identity:access:write` | Review a new delta or recover a pending change |
| `/backoffice/t/:tenant/access/:subject/changes` | POST | `identity:access:write` | Apply or resume the canonical change |

The corresponding identity-auth routes are under `/admin/access`; they are internal signed-workload endpoints. There is no public account or consumer endpoint for operator grants. GET detail and POST changes return JSON when `Accept: application/json` is set. Browser sessions and the same CSRF checks apply to JSON clients.

A JSON change body is:

```json
{
  "operation_id": "22222222-2222-4222-8222-222222222222",
  "expected_revision": "<revision returned by Inspect>",
  "reason": "Restore approved reporting access",
  "grant_roles": ["backoffice"],
  "revoke_roles": []
}
```

The route supplies the target subject. Body-supplied actor, issuer, tenant and target fields are rejected, as are unknown fields, duplicate scalar fields, duplicate role values and unknown roles. Checkbox role arrays are sorted once at the request boundary. The service checks the exact role delta, expected revision, idempotency and current
authority. Access mutations take a tenant lock before the shared enrollment identity
lock. Both locks and journal work share one SQL session, so bounded connection pools
cannot deadlock while a holder waits for another connection. Journal intent commits
before native writes; an interrupted operation remains explicitly pending.

Keycloak remains the authority for memberships and role grants. The SQL journal records access operations and audit history, including the original actor and verified source/request metadata. A currently authorized administrator can resume the same immutable pending operation; each recovery attempt records its actor and source separately without replacing the original actor. The durable enrollment receipt prevents signup from restoring explicitly revoked user access. Administrative user revocation records suppression before changing Keycloak, including when no prior receipt exists. An explicit administrator grant can restore the user role; it does not erase revocation history.

A pending change cannot be replaced with a new operation ID. Another currently
authorized administrator can resume its exact immutable terms; original actor and
recovery actor remain separate audit entries. An unrelated external role change is
preserved. Completion requires every requested grant to be present and every
requested revoke absent, and records actual `completed_roles` separately from the
immutable planned `after_roles`. Unknown mappings, uncertain native results or
unsatisfied requested deltas remain pending. Resume the same operation after fixing
the underlying authority/transport issue; do not delete the journal or receipt.

Removing the last enabled tenant administrator is rejected, including on pending retries. Disabled identities are shown in the access view and do not count as surviving administrators.
An administrator with a pending administrator-role revocation is not counted as the
surviving administrator for another change. If an independent native authority
change removes all other administrators during recovery, the pending command stays
blocked; an independently authorized, explicitly audited authority repair is needed
before recovery. Initial bootstrap cannot be reopened as a repair path.

Existing access tokens can retain removed roles until refresh or expiry (currently up to 300 seconds). The interface states this delay. This workflow does not claim immediate session-wide revocation and does not alter financial maker/checker rules.

The old whole-membership-set mutation command rejects writes before reading credentials or making network requests. Its dry-run inspection remains available. Initial administrator bootstrap is a separate reviewed operation using the canonical service and an explicitly resolved subject. The standalone operator uses the explicit handle `noebs-admin` and email `admin@noebs.sd`; it must not have the consumer user role. Native required-action email completes email verification, password and authenticator setup. Its exact private `/backoffice/setup-complete` return redirects to the existing OIDC login without establishing a session or trusting the action-status query. Bootstrap is not a permanent unauthenticated HTTP route or an email-to-role automatic rule.


For an authenticated JSON client, use the same private endpoint and session/CSRF
boundary as the form. Save the exact request above in a private file, using the
revision from the selected subject's detail response. For example:

```sh
curl --fail-with-body --cookie /private/operator.cookies \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Origin: https://noebs-workers.tail09832.ts.net' \
  --header 'X-CSRF-Token: <session CSRF token>' \
  --data-binary @/private/access-change.json \
  'https://noebs-workers.tail09832.ts.net/backoffice/t/noebs/access/<subject>/changes'
```

Render the separately reviewed bootstrap Job and its restrictive network policies
from the checked-in tool. The image must be the exact deployed release digest:

```sh
python3 infra/scripts/tenant_access_bootstrap.py \
  --namespace noebs --tenant noebs --operation-id <fixed canonical UUID> \
  --reason-file <private reason file> \
  --image ghcr.io/noebs/noebs@sha256:<deployed release digest> \
  --tenant-catalog infra/kubernetes/keycloak-authority/tenant-catalog.yaml \
  --output <new private manifest path>
```

The renderer writes a new mode-0600 manifest and never applies it. It includes an
operation-specific ServiceAccount with the existing GHCR pull secret, immutable
reason Secret, exact DNS/PostgreSQL/Keycloak egress and matching ingress policies,
and a nonroot Job with no service-account token, no restart and no automatic retry.
There is no direct SMTP or public network egress. Review and apply that exact file
separately after the normal release and migration succeed. On retry retain the
operation ID, reason and immutable Secret; replace only the exact owned finished
Job. Capture its result and bootstrap receipt, then explicitly delete only that
operation's Job, ServiceAccount, reason Secret and NetworkPolicies. Never remove
SQL journals or authority records as job cleanup.

Initial operator bootstrap runs separately from normal promotion, with a fixed
operation ID and an accountable reason file. It requires both the dedicated native
enroller credential and the **identity_auth_migrate** database login/owner; the
ordinary runtime role cannot write bootstrap state. It first reserves an immutable
per-tenant bootstrap record before creating an identity or changing required actions.
The native identity receives the admin-only operation marker atomically on CREATE.
An existing identity is accepted only with the reviewed exact `--expected-subject`,
or the matching operation-owned creation marker after a crash. A preclaimed email
or handle does not become an administrator automatically.

```sh
noebs bootstrap-tenant-admin \
  --config /app/config.yaml --service /app/service.yaml \
  --secrets /app/secrets.yaml \
  --database-secrets /app/migration-secrets.yaml \
  --tenant-catalog /app/tenant-catalog.yaml --tenant noebs \
  --operation-id <fixed canonical UUID> --reason-file /operation/reason
```

The command is fixed to `admin@noebs.sd` / `noebs-admin`. It requires hosted email
verification, password and authenticator setup, dispatches a native action email,
and journals `grant_roles=[backoffice,tenant-admin]`, `revoke_roles=[user]`.
The explicit no-op user revoke creates permanent signup suppression. It never
silently verifies an email, supplies a temporary password, merges an existing mobile
identity or creates a wallet. The native action email returns to the private
`/backoffice/setup-complete` landing, which starts the existing hosted sign-in flow.
The recipient needs tailnet access to reach that return URL.

Keep the resulting immutable subject and operation audit in the deployment receipt;
future synthetic smoke cleanup must preserve this exact identity. An interrupted
bootstrap resumes the same operation ID and terms. After the subject/actions are
persisted, retries do not call identity preparation again. A crash after successful
email dispatch but before its receipt is saved can send one additional setup email;
completed retries do not send mail or reset required actions. The permanent bootstrap
marker remains even if native roles are later removed, and a completed replay never
restores those roles.

Migration `006_tenant_access.sql` adds append-only intent/recovery history and the
separate bootstrap reservation. Existing memberships, profiles, wallets and receipt
values are retained. The only managed permission additions are access read/write in
the existing tenant-admin bundle. No user is reclassified by migration. Application
rollback must retain these tables and enrollment receipts; destructive down migrations
are rejected. Grant another administrator explicitly before removing one.

Run native PostgreSQL authority/recovery tests with `PG_BIN` pointing to PostgreSQL
18 binaries (and `PG_LIBDIR` when using a bundled runtime):

```sh
PG_BIN=/path/to/postgresql/18/bin scripts/test-tenant-access-postgres.sh
```

The runner creates and removes its own fixture, uses the real migrator/runtime roles,
and tests atomic denial, retries, concurrent administrators, bootstrap crash windows
and a one-connection pool. The pinned native Keycloak suite includes actual composed
roles, revocation, operation-owned creation, action email, password and TOTP setup.
