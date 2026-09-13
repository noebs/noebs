# Mail on an existing server

`deploy.py` manages one Stalwart installation independently of the exe.dev fleet.
It does not create a VM, join Kubernetes, alter the existing web proxy, or change
DNS records. The same native Stalwart installation serves every domain and
mailbox listed in its deployment configuration. Separate DNS, HTTPS proxy and
readiness tools complete the setup without changing unrelated services.

`deployment.yaml` is the checked-in desired state for the existing Contabo host;
`deployment.yaml.example` documents the same explicit schema. Both pin the immutable
Stalwart `v0.16.21` image. Its resource limits bound the mail process alongside
the host's other applications. Mail binds the explicit public IPv4 address on
ports 25, 465, 587 and 993. The HTTP listener binds only `127.0.0.1:18080`; the
existing HTTPS proxy serves `https://mail.noebs.sd` through that listener.
The proxy must preserve the public host and protocol and apply its own trusted
forwarder configuration. The HTTP listener contains both JMAP and administrative
APIs; publishing it through HTTPS exposes authenticated administration as well.

The public hostname needs a matching A record and Contabo PTR. Each mail domain
needs its own MX, SPF, DKIM and DMARC records. Existing mail routing must be
deliberately cut over after checking the new service. Native Stalwart DNS-01
certificate management uses a Cloudflare token limited to the managed zones;
it does not need to take over ports 80 or 443. Docker publishing can bypass host
firewall policies, so verify the actual exposed ports after setup.

## Configuration and secrets

Copy the public deployment example and set the explicit domains and mailboxes.
The native configuration module validates account ownership and the matching
secret inventory. Credentials belong in a SOPS encrypted YAML document:

```yaml
admin_password: <strong generated administrator password>
mailbox_passwords:
  noreply@noebs.sd: <strong generated application password>
cloudflare_api_token: <DNS token for the managed zones>
webmail_des_key: <persistent 24-character client session encryption key>
```

The mailbox keys above are illustrative: the actual mapping must exactly match
the configured mailbox accounts. The repository's `.sops.yaml` covers files with
`secrets` in their name. Use the same SOPS recipient policy as the other deployment
inputs, and keep the age identity private with mode 0600.

The operator needs Python 3, PyYAML, OpenSSH and SOPS. The existing server needs
Python 3, Docker with Compose 2.30 or newer, `ip`, `tar`, `openssl`, `iptables`,
systemd, and passwordless `sudo` for the chosen administrative account. An existing Tailscale SSH wrapper can be passed with
`--ssh`; normal OpenSSH agent authentication also works. Host key checking stays
strict, using the SSH client's existing known_hosts file or an explicit
`--known-hosts` path. Add `--identity` when a dedicated SSH key is used.

```sh
python3 infra/mail/deploy.py check \
  --config /private/mail/deployment.yaml \
  --secrets /private/mail/mail.secrets.yaml \
  --age-key /private/mail/age-key.txt

python3 infra/mail/deploy.py apply \
  --config /private/mail/deployment.yaml \
  --secrets /private/mail/mail.secrets.yaml \
  --age-key /private/mail/age-key.txt \
  --receipt /private/mail/applied.json
```

`check` validates and renders without contacting the server. `apply` validates
before SSH, takes a dedicated mail maintenance lock, checks the host addresses
and existing port ownership, and pulls the pinned image. It then briefly enters
Stalwart's native recovery mode, reconciles the database configuration, and starts
the normal services. Roundcube stops while Stalwart is in recovery. Recovery
exposes only the local HTTP endpoint. A rejected
native configuration does not reopen public mail ports.

The private receipt records the image, host and safe native summary, including
each domain's actual DKIM selector and public TXT record. Its parent directory
must have mode 0700. The command may update its own same-host receipt; it refuses
to overwrite an unrelated file.

The normal container has no recovery credential environment. Plaintext credentials
travel through SSH stdin, and the temporary recovery credential file is removed
after the attempt. Stalwart owns the persistent native accounts, DKIM keys and
certificates in its data directory. The wrapper refuses to recreate missing
authority for an installation with an existing deployment receipt.

An applied runtime is not evidence that remote mail providers accept deliveries.
Check DNS, reverse DNS, certificate issuance, authenticated submission, relay
rejection, mailbox access and a deliberate delivery test before routing real mail
or configuring Keycloak to use `mail.noebs.sd:465` with implicit TLS.

## Personal mail and browser access

Open [webmail.adonese.sd](https://webmail.adonese.sd/) and sign in with the full
mailbox address. The configured personal and role addresses are:

| Domain | Personal | Information | Automated mail |
| --- | --- | --- | --- |
| noebs.sd | adonese@noebs.sd | info@noebs.sd | noreply@noebs.sd |
| adonese.sd | adonese@adonese.sd | info@adonese.sd | noreply@adonese.sd |
| 2t.sd | adonese@2t.sd | info@2t.sd | noreply@2t.sd |

Each address is an independent inbox. Each domain also has a `postmaster`
mailbox; `postmaster@noebs.sd` is the explicit server administrator. NoEBS uses
`noreply@noebs.sd` for Keycloak account messages. These SMTP credentials belong
only to that ordinary mailbox and grant no server administration rights.

The pinned official Roundcube 1.7.4 client uses Stalwart for authentication,
messages and sending. Its SQLite database holds contacts, preferences and
sessions. It runs as UID 33, binds only `127.0.0.1:18089`, and verifies Stalwart's
TLS certificate for both IMAP and SMTP. Attachments are limited to 10 MiB and
messages to 20 MiB. The existing Caddy proxy supplies public HTTPS.

Phone and desktop clients use the same mailbox address and password:

| Setting | Value |
| --- | --- |
| IMAP server | mail.noebs.sd |
| IMAP port/security | 993, SSL/TLS |
| SMTP server | mail.noebs.sd |
| SMTP port/security | 465, SSL/TLS; alternatively 587, STARTTLS |
| Authentication | Required; full email address |

Initial passwords are encrypted in `deployment.secrets.yaml`. An authorized
operator can retrieve a specific initial credential with:

```sh
sops --decrypt --extract '["mailbox_passwords"]["adonese@adonese.sd"]' \
  infra/mail/deployment.secrets.yaml
```

Use the [Stalwart account manager](https://mail.noebs.sd/account) for password
changes, two-factor authentication and application passwords. The explicit
`Initial` password policy on personal and information mailboxes preserves later
user changes during deployment. `Managed` keeps the postmaster and automated
mail credentials synchronized with SOPS. Both policies preserve enrolled TOTP
and separate app passwords. The Roundcube session encryption key stays stable
across deployments; changing it requires an explicit rotation.

## DNS, HTTPS and readiness

`dns.py` reads the same public configuration and SOPS credentials. It prints the
concrete changes before applying them, and leaves unrelated records intact.
`--hostname-only` prepares the mail and webmail A records before certificate issuance.
Full publication requires the native DKIM records and manages MX, SPF and DMARC
for every configured domain. DNS credentials stay in the SOPS input; pass the
age identity with the standard `SOPS_AGE_KEY_FILE` environment variable.

Publish only the hostname before the first deployment so native certificate
issuance can resolve it:

```sh
SOPS_AGE_KEY_FILE=/private/mail/age-key.txt \
  python3 infra/mail/dns.py \
  --config /private/mail/deployment.yaml \
  --secrets /private/mail/mail.secrets.yaml \
  --hostname-only --apply
```

After deployment, use its receipt to plan the complete mail routing. Add `--apply`
to execute the reviewed changes:

```sh
SOPS_AGE_KEY_FILE=/private/mail/age-key.txt \
  python3 infra/mail/dns.py \
  --config /private/mail/deployment.yaml \
  --secrets /private/mail/mail.secrets.yaml \
  --receipt /private/mail/applied.json
```

The receipt must match the configured host, image, domains and current selector.
Each configured domain needs its own generated DKIM record. The plan preserves
unrelated website addresses and TXT records, including previous DKIM selectors.
It removes stale IPv6 addresses only at the two explicitly managed hostnames.
Cloudflare's flattened apex CNAMEs remain in place alongside the MX and TXT
records, preserving the existing websites.

Use `proxy.py` for the existing host's HTTPS proxy integration. The Contabo host
runs that Caddy instance as a Kubernetes deployment; the inactive systemd Caddy
service is not the traffic authority. The integration must preserve the existing
web routes and add only the explicitly configured mail and webmail hostnames.

```sh
python3 infra/mail/proxy.py apply \
  --config infra/mail/deployment.yaml \
  --namespace edge --deployment caddy --container caddy \
  --configmap noebs-migration-298af9fe5490-forward \
  --config-key Caddyfile --server srv0 --restart-proxy
```

The integration checks the live configuration against its ConfigMap, validates
and gracefully loads the new configuration, and persists it for future pods.
`--restart-proxy` performs the existing Recreate deployment's brief restart to
refresh its subPath mount, then verifies the full configuration. It preserves
all unrelated routes. Use `check` instead of `apply` to inspect the change.

Stalwart renews its SMTP/IMAP certificate through native Cloudflare DNS-01. Its
certificate-only domain uses a DKIM-only publication allowlist and has no signing
keys; this is the supported narrow policy for the pinned version's required
nonempty allowlist. It publishes no CAA records that could restrict Caddy's
separate HTTPS issuer. DNS publication for the actual mail domains belongs only
to `dns.py`.

The managed `noebs-mail-firewall.service` adds only the four mail-port exceptions
for the explicit public interface and original destination IPv4 in DOCKER-USER.
Existing public Docker deny rules remain in place. Systemd reapplies the narrow
exceptions after Docker and its existing firewall service restart.

After publishing the records and setting the provider PTR, check the public
service without submitting or delivering a message:

```sh
python3 infra/mail/verify.py \
  --hostname mail.noebs.sd --ipv4 213.199.63.78 \
  --domain noebs.sd --domain adonese.sd --domain 2t.sd \
  --dkim noebs.sd:noebs2026 \
  --dkim adonese.sd:noebs2026 \
  --dkim 2t.sd:noebs2026
```

Use the selectors from the applied receipt if they differ from this example.
The checks cover forward and reverse DNS, SPF, DMARC, DKIM, TLS on SMTP and IMAP,
and rejection of unauthenticated external relay attempts. A separate authorized
mailbox login and delivery test checks the account credentials and final delivery.

## Backup and recovery

Backups are explicit maintenance operations; this setup creates no schedule.
The backup holds the same lock as deployment, stops Roundcube before Stalwart,
streams their configuration and data through SSH directly into local age
encryption, and resumes only the services that were previously running, even if
archiving fails. It starts Stalwart before Roundcube. No plaintext archive is written locally.

Create a private output directory and provide an explicit public age recipient:

```sh
python3 infra/mail/deploy.py backup \
  --config /private/mail/deployment.yaml \
  --recipient age1... \
  --output /private/mail/backups/stalwart-2026-09-13.tar.age
```

An existing output file is never overwritten. Store completed encrypted archives
away from the mail host and retain the corresponding age identity separately.
The archive contains the native database, messages, DKIM keys, certificates,
startup configuration, image pins, public deployment receipt, firewall helper,
and Roundcube database, configuration and session encryption key. Before an image
upgrade, take a backup and confirm it can be decrypted and its tar contents listed.

Restore with both services stopped and the mail maintenance lock held. Restore the
archive's configuration, data and manifests into the configured state directory,
preserving UID 2000 for Stalwart and UID 33 for Roundcube, then start the recorded
images. Run the normal apply command to reinstall the managed firewall service. Do not initialize an empty
database in place of missing state or assume an older binary can read a database
upgraded by a newer release. Validate the restored private API and mail protocols
before returning traffic.

## Validation

```sh
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=infra/mail \
  python3 -m unittest discover -s infra/mail -p 'test_*.py'
```

The container contract follows the tagged
[Stalwart Dockerfile](https://github.com/stalwartlabs/stalwart/blob/v0.16.21/Dockerfile)
and its [declarative deployment model](https://stalw.art/docs/configuration/declarative-deployments/).
Contabo provides [PTR management](https://api.contabo.com/#tag/DNS) independently
of the domain's authoritative DNS provider.

Roundcube references: [1.7.4 security release](https://roundcube.net/news/2026/09/06/security-updates-1.6.19-and-1.7.4)
and [official image configuration](https://github.com/roundcube/roundcubemail-docker/blob/d7832f788dc097b109f89f4b4d37682edbec788c/README.md).
