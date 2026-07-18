# Isolated alpha HTTP E2E

`scripts/alpha-http-e2e.sh` exercises a fresh noebs build through the real API
gateway and real HTTP service boundaries. It is intended for release-candidate
checks on the Tailscale deployment host, not for production traffic.

Run it from a clean, frozen checkout:

```sh
./scripts/alpha-http-e2e/test.sh
./scripts/alpha-http-e2e.sh
```

The harness builds and labels the current commit, then starts only these roles:

- `api-gateway`
- `identity-auth` and its migration role
- `card-vault` and its migration role
- `consumer-beneficiary` and its migration role

It creates three fresh Postgres databases in a temporary cluster and puts every
container on an internal Docker network with no published ports. A small client
inside that network drives real HTTP through the gateway. The Postgres and
capture images are pinned by manifest digest and are pulled only when missing.
EBS, wallet, PSP, reporting, notification, and Keycloak targets resolve to a
deny-by-default capture service. Any unexpected HTTP request or wallet-ledger
TCP connection to those boundaries fails the run.

The HTTP journey covers disposable registration; authenticated, one-time OTP
capture; pre-verification login rejection; OTP replay rejection; login;
authenticated identity reads; refresh rotation and replay rejection; password
change; a tiny synthetic KYC fixture;
card create/read/update/main/delete; beneficiary create/update/read/delete; and
zero-amount payment-link create/read. The removed `/consumer/payment_request`
route is also required to remain unavailable.

No real phone number, card, SMS gateway, EBS endpoint, funds, wallet, PSP, or
shared database is used. Generated keys, passwords, JWTs, the captured OTP, and
HTTP response bodies remain in shell memory or under a mode-`0700` directory in
`/dev/shm`. Service output is not printed because it can contain request details.
The exit trap removes containers, temporary databases, the test image, and all
memory-backed files on success, failure, or interruption.

The script deliberately refuses a dirty checkout. Commit or isolate the exact
release candidate first; this prevents an operator from accidentally certifying
an unreviewed working tree.
