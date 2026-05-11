# Mail Server Setup

Brygge runs its own mail server on the same NixOS VM as the API:

- **Stalwart** — all-in-one SMTP + IMAP + JMAP server (Rust). Handles inbound mail, outbound relay, role mailboxes (`treasurer@<domain>`, `secretary@<domain>`, etc.), and DKIM signing.
- **Bulwark** — JMAP webmail client (Next.js, via official container). Accessed at `webmail.<domain>`.
- **Caddy** — terminates TLS for HTTPS traffic (JMAP, admin UI, webmail). Stalwart terminates TLS itself for SMTPS/IMAPS.

Role mailbox data lives on the server in RocksDB, outlasting any individual board member.

---

## Architecture

```
                    ┌───────────────────────────────────────────────┐
                    │              Hetzner CX23 VM                  │
                    │                                               │
  smtp :25      ────┼──▶ Stalwart (Rust)                            │
  smtps :465    ────┼──▶ SMTP/IMAP/JMAP server                      │
  submission :587   │   • RocksDB storage                           │
  imaps :993    ────┼──▶ • DKIM signer (rspamd-equivalent built-in) │
                    │   • Spam filter                               │
                    │                                               │
                    │     HTTP (JMAP+admin) on 127.0.0.1:8088       │
                    │                 ▲                             │
                    │                 │                             │
  https :443    ────┼──▶ Caddy ───────┤ reverse-proxy               │
                    │   (TLS)         │                             │
                    │                 └──▶ podman: bulwark (3000)   │
                    │                                               │
                    │   security.acme → /var/lib/acme/mail.<dom>/   │
                    └───────────────────────────────────────────────┘

  Public DNS (Hetzner Cloud DNS, managed by terranix):
    @          MX  10 mail.<domain>
    mail       A   <server IP>          (plus rDNS PTR → mail.<domain>)
    webmail    A   <server IP>
    @          TXT v=spf1 mx include:amazonses.com -all
    mail._domainkey TXT v=DKIM1; k=rsa; p=...
    _dmarc     TXT v=DMARC1; p=quarantine; rua=mailto:<admin>; fo=1
    autoconfig CNAME mail.<domain>
    _imaps._tcp      SRV 0 0 993 mail.<domain>.
    _submission._tcp SRV 0 0 587 mail.<domain>.
```

All config lives in `nix/host.nix` under `services.stalwart` and the Caddy/oci-containers blocks. Per-club values (domain, admin email, ACME contact) come from `terraform/terraform.tfvars.json` via the flake's `clubConfig`.

---

## First-time setup

Prerequisites:

- Base [deploy flow](../deploy.md) completed up through the NixOS bootstrap and brygge itself running
- `mail.<domain>` and `webmail.<domain>` A records published (handled by `nix run .#tf-apply` — included in the standard deploy DNS set)
- Port 25 outbound unblocked by Hetzner (see [below](#hetzner-port-25))

### 1. Deploy the mail stack

```bash
nix run .#deploy -- <server-ip>
```

The first deploy with Stalwart takes ~10-15 min (new container images for Bulwark + Stalwart binary download). Subsequent deploys are much faster.

### 2. Create the bootstrap admin password

Stalwart doesn't auto-generate an admin password. Create one on the server:

```bash
PASS=$(openssl rand -base64 24)
echo "Stalwart admin password: $PASS   (save this)"

# Note: `printf '%s'` avoids the trailing newline that `<<<` would add.
# Stalwart compares the file byte-for-byte against the login input, so a
# trailing newline would make the password fail authentication.
printf '%s' "$PASS" | ssh root@<server-ip> "install -d -m 0700 -o root /etc/stalwart && install -m 0400 -o root /dev/stdin /etc/stalwart/admin-password"

ssh root@<server-ip> 'systemctl restart stalwart'
```

Verify systemd loaded the credential:

```bash
ssh root@<server-ip> 'ls /run/credentials/stalwart.service/'
# should list: admin_password
```

### 3. Log in to Stalwart admin

Open `https://mail.<domain>/` and log in as `admin` + the password from step 2.

First action in the admin UI:

**Configuration → Authentication → Principals** → create a real admin user with a new password. Optionally delete/rotate the bootstrap `admin` fallback afterwards.

DKIM is **not** configured through the UI — it's provisioned declaratively by the `stalwart-dkim-config` systemd service (defined in `nix/host.nix`). See the next two steps for the keypair-and-tfvars side of that flow.

### 4. Provision DKIM

Without a fixed-selector DKIM signature pinned in DNS, Stalwart can sign with rotated selectors that have no DNS counterpart, so receivers see `dkim=permerror` and DMARC passes only via the weaker SPF leg — which weighs into spam scores at every major receiver and disqualifies the message from BIMI rendering on stricter providers (Yahoo / Apple).

The fix is to pin a single fixed signature with selector `mail` and publish its public key at `mail._domainkey.<domain>`. The flow under Stalwart **0.15.x** is:

- **Stalwart side**: `nix/host.nix` → `systemd.services.stalwart-dkim-config` runs after every boot and idempotently calls `stalwart-cli dkim create rsa <domain> mail mail`. Stalwart **generates and owns the keypair internally** — there is no way to import an externally-managed private key via the 0.15 CLI.
- **DNS side**: `terraform/dns.nix` → `mail_dkim` publishes the public key at `mail._domainkey.<domain>`, sourced from `tfvars.dkim_public_value`. The operator copies the public half out of Stalwart and pastes it into tfvars.

Operator one-time setup per club:

```bash
# 1. Deploy. The systemd unit creates the DKIM signature inside
#    Stalwart on first run (idempotent — repeat runs are no-ops).
#    Stalwart 0.15 generates a 2048-bit RSA keypair internally; there
#    is no flag to choose another size.
nix run .#deploy

# 2. Pull the generated public key out of Stalwart.
KEY=$(ssh root@mail.<domain> -- stalwart-cli dkim get-public-key mail \
        | sed -n 's/.*Public DKIM key for signature mail: "\(.*\)"/\1/p')

# 3. Chunk into ≤255-byte quoted substrings (Hetzner DNS enforces RFC
#    1035's 255-byte-per-string limit; Stalwart 0.15 generates 2048-bit
#    RSA keypairs whose base64 form is ~392 bytes — exceeds the limit
#    as a single string).
FULL="v=DKIM1; k=rsa; p=${KEY}"
TXT_VALUE=$(printf '%s' "$FULL" | awk '
  { s = $0; out = "";
    while (length(s) > 0) {
      chunk = substr(s, 1, 255); s = substr(s, 256);
      out = out (out=="" ? "" : " ") "\"" chunk "\"";
    } print out }')

# 4. Paste into tfvars.dkim_public_value. The value already contains
#    space-separated quoted substrings; just JSON-escape it.
jq --arg v "$TXT_VALUE" '.dkim_public_value = $v' \
  terraform/terraform.tfvars.json > /tmp/t && mv /tmp/t terraform/terraform.tfvars.json

# 4. Publish the public key in DNS.
nix run .#tf-apply
```

Verify the DNS record matches what Stalwart is signing with:

```bash
dig TXT mail._domainkey.<domain> @hydrogen.ns.hetzner.com +short
# expect: "v=DKIM1; k=rsa; p=MIG..."

ssh root@mail.<domain> -- stalwart-cli dkim get-public-key mail
# must match the p= value above byte-for-byte
```

Verify the systemd unit:

```bash
ssh root@<server-ip> -- systemctl status stalwart-dkim-config
ssh root@<server-ip> -- journalctl -u stalwart-dkim-config -n 50 --no-pager
# First run: "stalwart-cli dkim create rsa ... mail mail" exits 0.
# Subsequent runs: log line "DKIM signature already present — nothing to do."
```

End-to-end check: send a test message and look at the recipient's `Authentication-Results`. Both DKIM lines should read `dkim=pass header.s=mail`. If they show `header.s=YYYYMMr` / `YYYYMMe` instead, the unit didn't converge — see [BIMI guide § DKIM provisioning](bimi.md#dkim-provisioning-declarative) for troubleshooting.

### 5. Create human role mailboxes

In the admin UI → **Management → Accounts → New**:

- `info@<domain>` — a real inbox monitored by a board member (or rotating role). Set a password for the current officeholder.
- `treasurer@<domain>`, `secretary@<domain>`, `admin@<domain>`, etc. — shared role mailboxes for board members. Password per role.

Aliases: each mailbox can have multiple aliases configured in the same screen (e.g. `kasserer@<domain>` → treasurer).

These are the mailboxes humans log into via Bulwark webmail or any IMAP client.

### 6. Provision the brygge SMTP relay account

Brygge sends mail (magic links, invoices, broadcasts) as a dedicated `relay@<domain>` principal — separate from any human mailbox so that webmail-side renames or password changes can't break service mail.

The `relay@<domain>` principal is provisioned **declaratively** by the `stalwart-relay-account.service` systemd unit defined in `nix/host.nix`. You only have to supply its password as a server-side file:

```bash
ssh root@<server-ip> 'install -m 0400 -o root /dev/stdin /etc/stalwart/relay-password' <<< "<strong-password>"
ssh root@<server-ip> 'systemctl restart stalwart-relay-account.service'
```

The unit waits for Stalwart's admin API, then either creates the principal (first run) or updates its bcrypt secret (subsequent runs). It runs at every boot, so the principal stays in sync with the password file.

### 7. Wire brygge to SMTP

Edit `/etc/brygge/env` on the server:

```
SMTP_HOST=mail.<domain>
SMTP_PORT=465
SMTP_USERNAME=relay
SMTP_PASSWORD=<same value as /etc/stalwart/relay-password>
EMAIL_FROM=<Club Name> <relay@<domain>>
EMAIL_REPLY_TO=info@<domain>
```

Notes:

- **Port 465 (implicit TLS)**, not 587. Brygge's SMTP client supports both; 465 sidesteps an intermittent STARTTLS hang in current Stalwart.
- **`SMTP_USERNAME=relay`** — the bare principal name, not the email address. Stalwart's auth lookup uses the principal slug.
- **`SMTP_HOST=mail.<domain>`** — use the public hostname so the TLS SNI matches the cert. Don't use `localhost` or `127.0.0.1`.
- **`EMAIL_REPLY_TO`** sets the `Reply-To:` header so member replies land in the monitored `info@` inbox instead of the send-only `relay@` account.
- **`SMTP_PASSWORD` must equal the contents of `/etc/stalwart/relay-password`.** When rotating, update both files and restart `stalwart-relay-account.service` and `brygge.service`.

Restart brygge:

```bash
ssh root@<server-ip> 'systemctl restart brygge'
```

Test by requesting a magic link against `https://<domain>/api/v1/auth/magic-link`. The email should arrive in your inbox within ~30s.

### 8. First Bulwark login

Open `https://webmail.<domain>/` — Bulwark's login screen. Enter:

- Email: one of your role mailboxes (e.g. `admin@<domain>`)
- Password: the mailbox's password (not the Stalwart admin password — those are separate)

Bulwark connects via JMAP to `https://mail.<domain>` and presents the inbox.

---

## Day 2 operations

### Rotating a role mailbox password

When a board member changes role (e.g. new treasurer):

1. Stalwart admin UI → **Management → Accounts → `treasurer@<domain>`** → set new password
2. Hand the new password to the incoming officeholder
3. Archive is preserved — they see full mail history on first login

No NixOS deploy needed. Stalwart writes to its RocksDB immediately.

### Adding a new role

Same flow as step 5 — Stalwart admin UI handles everything. No code change.

### Rotating the brygge SMTP relay password

The `relay@<domain>` principal that brygge authenticates as is provisioned by `stalwart-relay-account.service` from `/etc/stalwart/relay-password`. To rotate:

```bash
NEW=$(openssl rand -base64 24)
ssh root@<server-ip> "install -m 0400 -o root /dev/stdin /etc/stalwart/relay-password" <<< "$NEW"
# Mirror the same value into brygge's env (line: SMTP_PASSWORD=...)
ssh root@<server-ip> 'systemctl restart stalwart-relay-account brygge'
```

The systemd unit re-bcrypts the new plaintext and PATCHes Stalwart's stored secret on every restart, so the principal stays in sync with the file. brygge picks up the new `SMTP_PASSWORD` from `/etc/brygge/env` on its restart.

### Rotating the DKIM keypair

The `mail`-selector keypair lives inside Stalwart's DB; DNS holds the public half. To rotate:

```bash
# 1. Delete the existing signature so the unit re-creates a fresh one.
#    (The 0.15 CLI has no `dkim rotate` shortcut; we do it manually
#    via the admin UI: Configuration → Authentication → DKIM →
#    delete the `mail` signature.)

# 2. Trigger the systemd unit to create a new one.
ssh root@mail.<domain> -- systemctl restart stalwart-dkim-config

# 3. Pull the new public key.
NEW_KEY=$(ssh root@mail.<domain> -- stalwart-cli dkim get-public-key mail)
jq --arg v "\"$NEW_KEY\"" \
  '.dkim_public_value = $v' \
  terraform/terraform.tfvars.json > /tmp/t && mv /tmp/t terraform/terraform.tfvars.json

# 4. Publish the new public key.
nix run .#tf-apply
```

There is a brief window between step 2 (Stalwart begins signing with the new key) and step 4 (DNS catches up) where in-flight mail will fail DKIM. For a low-volume club, accept the gap; for higher-volume domains, publish a second selector temporarily and migrate over.

### Replacing the bootstrap admin password

Once a real admin is set up via the UI, the file-based fallback in `/etc/stalwart/admin-password` is dormant but still there. Either:

- Leave it (only used if DB admin is deleted)
- Rotate it periodically:

```bash
NEW=$(openssl rand -base64 24)
printf '%s' "$NEW" | ssh root@<server-ip> "install -m 0400 -o root /dev/stdin /etc/stalwart/admin-password"
ssh root@<server-ip> 'systemctl restart stalwart'
```

### Backups

Critical data:

- `/var/lib/stalwart/data` — RocksDB with messages, accounts, DKIM keys, config
- `/var/lib/acme/mail.<domain>/` — TLS cert (security.acme regenerates, so this is recoverable)
- `/etc/stalwart/admin-password` — bootstrap credential

Daily snapshot via Hetzner's built-in snapshot feature (20% surcharge on server cost) covers the VM at filesystem level.

For logical backups:

```bash
ssh root@<server-ip> 'systemctl stop stalwart && tar -czf - /var/lib/stalwart/data' | gzip > stalwart-backup-$(date +%F).tar.gz
ssh root@<server-ip> 'systemctl start stalwart'
```

Stop/start is necessary because RocksDB won't produce a consistent snapshot while the server is running.

### Verifying deliverability

After initial deploy and for the first 2 weeks of real use (see [DIL-150](https://linear.app/dillonteknisk/issue/DIL-150)):

```bash
# Full check
open "https://mxtoolbox.com/SuperTool.aspx?action=mx%3a<domain>"

# Individual queries
dig MX <domain> +short
dig TXT <domain> +short    # SPF
dig TXT _dmarc.<domain> +short
dig TXT mail._domainkey.<domain> +short
dig -x <server-ip> +short  # rDNS → mail.<domain>.

# Send test via SMTP submission
# From a third-party, send a mail to yourself, check headers for:
#   dkim=pass header.d=<domain>
#   spf=pass
#   dmarc=pass
```

Use [mail-tester.com](https://mail-tester.com) to get a spam score — aim for 9+/10.

---

## Hetzner-specific gotchas

### Port 25

Hetzner blocks outbound TCP/25 by default. Open a support ticket after your first paid invoice — their system auto-approves for legitimate mail servers. Until then, outbound mail to external MX servers fails silently; SMTPS/Submission still works inbound.

### DKIM 255-byte limit

Hetzner Cloud DNS rejects TXT records with substrings over 255 bytes. 2048-bit RSA DKIM public keys exceed this. Use 1024-bit (set in step 3 above). Gmail, Outlook, and most MTAs still accept 1024-bit; it's deprecated but not rejected.

DIL-172 tracks a future upgrade to 2048-bit via either multi-record split or moving DNS off Hetzner Cloud DNS.

### IPv6 not auto-configured

Hetzner allocates a /64 IPv6 prefix per server, but NixOS's default DHCP doesn't assign a global IPv6 to the interface. We publish only IPv4 A records for `mail.<domain>` to avoid Let's Encrypt falling back to IPv6 and timing out. DIL-173 tracks wiring up IPv6 properly.

### rDNS

`46.225.99.41 → mail.<domain>` PTR is set via `hcloud_rdns` in `terraform/server.nix`. If you change server IP or domain, terraform will update the PTR on next `tf-apply`. Gmail/Outlook heavily penalize unmatched rDNS.

---

## Troubleshooting

### "Incorrect password" when logging into Stalwart admin

Most common cause: the password file has a trailing newline. Recreate it with `printf '%s'` (not `<<<` or `echo`):

```bash
printf '%s' "$PASS" | ssh root@<server-ip> "install -m 0400 -o root /dev/stdin /etc/stalwart/admin-password"
ssh root@<server-ip> 'wc -c /etc/stalwart/admin-password'  # byte count == password length
ssh root@<server-ip> 'systemctl restart stalwart'
```

Second most common: the Nix change wasn't deployed yet. Verify systemd has the credential mapping:

```bash
ssh root@<server-ip> 'systemctl cat stalwart | grep LoadCredential'
# expect: LoadCredential=admin_password:/etc/stalwart/admin-password
```

If missing, `nix run .#deploy -- <server-ip>` again.

### `mail.<domain>` returns brygge's home page

Port collision — Stalwart and brygge both wanted `127.0.0.1:8080`. Stalwart's HTTP listener is now on `127.0.0.1:8088` (see `nix/host.nix`). If you see brygge's CSP headers on `curl -sI https://mail.<domain>/`, double-check the Stalwart `server.listener.http.bind` setting and the Caddy vhost's `reverse_proxy` port match.

### DKIM TXT record rejected by Hetzner API

Hetzner Cloud DNS requires exactly one double-quoted string per TXT record, ≤255 bytes. Common errors:

- `a TXT string exceeds 255 characters` → DKIM key too big. Use 1024-bit.
- `TXT records must be fully escaped with double quotes` → value must be wrapped in outer quotes, no internal `" "` substring separators.
- Record exists but `dig` returns garbled content with `\010` or `\n` — JSON string has a literal newline. Use `printf` instead of pasting; see step 4.

Build the tfvars value with jq's `--arg` to get proper escaping:

```bash
jq --arg v "$VALUE" '.dkim_public_value = $v' tfvars.json
```

### Can't log in to Bulwark

Bulwark authenticates via JMAP against `https://mail.<domain>`. Failure modes:

- Wrong credentials → you're using the **Stalwart admin** password, not the mailbox password. These are separate. Use the password you set in the "New Account" screen in Stalwart admin.
- "JMAP_SERVER_URL not configured" / infinite spinner → the Bulwark container isn't reaching Stalwart. `ssh root@<server-ip> 'podman logs bulwark | tail -30'`.
- CSP/CORS errors in browser devtools → Stalwart's JMAP needs `connect-src 'self'` allowance from Bulwark's domain. Default config should handle this.

### Stalwart logs full of "Database key defined in local configuration"

Informational — Stalwart prefers most runtime config to live in its own DB (editable via admin UI) rather than in the TOML. Doesn't affect behavior.

Long-term fix is to move those settings to the DB via the admin UI and remove them from `nix/host.nix`. Tracked as future work; for now they're cosmetic warnings.

### "Temporary failure in name resolution" during activation

Don't enable `mailserver.localDnsResolver` (knot-resolver). It breaks upstream DNS resolution during boot. Not relevant to Stalwart directly but noted here because it bit us during the simple-nixos-mailserver phase.

---

## What's managed where

| Thing | Managed by | Touch via |
|---|---|---|
| Server, firewall, DNS records | Terranix | `nix run .#tf-apply` |
| NixOS config (Stalwart, Caddy, Bulwark) | Nix flake | `nix run .#deploy -- <host>` |
| Stalwart admin settings (spam, TLS) | Stalwart RocksDB | Admin UI at `https://mail.<domain>/` |
| DKIM signature (`mail` selector) | Stalwart RocksDB (Stalwart owns the keypair) + `tfvars.dkim_public_value` mirrored to DNS | `deploy` creates the signature, then `stalwart-cli dkim get-public-key mail` → paste to tfvars → `tf-apply` (see § 4) |
| Mailbox accounts + passwords | Stalwart RocksDB | Admin UI |
| Mail contents | Stalwart RocksDB | IMAP / JMAP clients |
| Bootstrap admin password | `/etc/stalwart/admin-password` on the server | `install` + `systemctl restart stalwart` |
| brygge's SMTP credentials | `/etc/brygge/env` on the server | Edit + `systemctl restart brygge` |

---

## Related

- [docs/deploy.md](../deploy.md) — overall deploy guide
- [docs/mail/bimi.md](bimi.md) — publishing the club logo for inbox rendering (DMARC + DKIM prerequisites covered there in detail)
- [DIL-141](https://linear.app/dillonteknisk/issue/DIL-141) — parent feature (self-host mail)
- [DIL-166](https://linear.app/dillonteknisk/issue/DIL-166) — first-deploy workflow simplification
