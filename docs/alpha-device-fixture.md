# Isolated alpha device fixture

This fixture is for an operator-controlled Android QA session, not external
alpha testers. The production `tenant-cutover` signup and recovery journeys
remain blocked until identity-auth has a reviewed real SMS provider or provider
sandbox; never compensate by inserting users, OTPs, or sessions in the live
database.

The fixture runs the same candidate image and migrations as the isolated HTTP
journey, with separate in-memory secrets and databases. Only its API gateway is
published, on a loopback port. OTP capture stays inside the Compose network and
can be consumed only from the foreground operator terminal. EBS, wallet, PSP,
reporting, notification, and Keycloak endpoints remain side-effect guards.

## Preconditions

- Use a clean checkout of the reviewed candidate commit. The fixture rejects an
  image whose OCI revision label does not equal that exact commit.
- Use the immutable GHCR digest produced by the bounded local release build.
- Do not start this fixture until the Android QA artifact has a reviewed build
  flag that disables Firebase Analytics, Sentry delivery, FCM token retrieval,
  and device-token registration. Compose guards cannot observe direct
  device-to-third-party traffic. Android commit `872f12d5` is the minimum
  accepted fixture revision; it makes telemetry-off mandatory for every
  `alpha-device-*` tenant.
- Confirm the deployment host has at least 2 GiB available memory. The fixture
  containers remain capped by `scripts/alpha-http-e2e/compose.yaml`; the API
  gateway is the only published service and binds to `127.0.0.1`.
- Choose a disposable exact tenant such as `alpha-device-260718-a`. Only the
  `alpha-device-*` namespace is accepted; it must not equal or prefix a
  production tenant.

Validate the non-secret inputs without starting anything:

```sh
scripts/alpha-device-fixture.sh check \
  alpha-device-260718-a \
  ghcr.io/noebs/noebs@sha256:<64-hex-digest> \
  18080
```

## Start and route

In terminal A on the deployment host, start the foreground fixture:

```sh
scripts/alpha-device-fixture.sh start \
  alpha-device-260718-a \
  ghcr.io/noebs/noebs@sha256:<64-hex-digest> \
  18080
```

Wait for `READY`. The terminal accepts only `otp`, `status`, and `stop`.
`otp` consumes one captured challenge and prints its six-digit value; it does
not expose the capture service or read token.

In terminal B, render the exact Caddy matcher:

```sh
scripts/alpha-device-fixture.sh matcher alpha-device-260718-a 18080
```

Review the output, insert it immediately before the unqualified
`api.noebs.sd` reverse proxy in `deploy/kubernetes/edge/Caddyfile`, and release
that temporary change through the foundation-owned `noebs-edge` Argo
Application. Do not apply an untracked live patch. The matcher accepts only the
exact tenant header and the `/test`, `/app/config`, and `/consumer/*` paths;
every other path carrying that tenant is rejected before the production gateway.
Asset links, payment fallbacks, capture reads, and every other host remain on
their normal routes. Chat `/ws` is intentionally unsupported and fails closed.

From the Android app checkout, use this PowerShell build contract exactly:

```powershell
git merge-base --is-ancestor 872f12d5 HEAD
if ($LASTEXITCODE -ne 0) { throw 'Android fixture telemetry gate is missing' }
if (git status --porcelain) { throw 'Android fixture checkout is dirty' }
$env:TUTIPAY_API_URL = 'https://api.noebs.sd/'
$env:TUTIPAY_NOEBS_URL = 'https://api.noebs.sd/'
$env:TUTIPAY_TENANT_ID = 'alpha-device-260718-a'
$env:TUTIPAY_TELEMETRY_ENABLED = 'false'
.\gradlew.bat :app:assembleDebug --no-daemon
if ($LASTEXITCODE -ne 0) { throw 'telemetry-disabled QA build failed' }
if (-not (Test-Path 'app/build/outputs/apk/debug/app-universal-debug.apk')) {
    throw 'expected universal QA APK is missing'
}
```

Gradle rejects a device-lab tenant unless telemetry is disabled. The expected
artifact is `app/build/outputs/apk/debug/app-universal-debug.apk`. Before
installing it, clear any retained tenant, JWT, user, and device-key state, and
fail the session if clearing or installation fails:

```powershell
$package = 'com.tutipay.app.alpha'
adb shell pm path $package *> $null
if ($LASTEXITCODE -eq 0) {
    adb shell pm clear $package
    if ($LASTEXITCODE -ne 0) { throw 'failed to clear existing alpha app state' }
}
adb install --replace --grant-all app/build/outputs/apk/debug/app-universal-debug.apk
if ($LASTEXITCODE -ne 0) { throw 'failed to install telemetry-disabled QA APK' }
```

Exercise signup with device-signed OTP verification, signed OTP login, recovery
with a newly generated device key, old-session revocation, password login,
profile, language, menus, and empty card/transfer states. Do not enter a real
PAN, PIN, IPIN, or initiate a transfer. The foreground session fails at teardown
if a guarded external boundary was contacted.

## Teardown

Remove the temporary matcher from Git first and wait for `noebs-edge` to become
Synced and Healthy. Then type `stop` in terminal A (or send SIGINT). The trap
removes and verifies the exact Compose project, containers, volumes, network,
memory-backed runtime, and one-time secrets. If removal or verification fails,
the command exits nonzero and retains the mode-`0700` runtime for operator
recovery. It retains the immutable candidate image. Confirm the loopback port is
closed and no `noebs-alpha-*` containers from the printed project remain, then
clear and uninstall the QA app, failing closed if either command fails:

```powershell
adb shell pm clear com.tutipay.app.alpha
if ($LASTEXITCODE -ne 0) { throw 'failed to clear QA app state after testing' }
adb uninstall com.tutipay.app.alpha
if ($LASTEXITCODE -ne 0) { throw 'failed to uninstall QA app' }
```
