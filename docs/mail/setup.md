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

First actions in the admin UI:

1. **Configuration → Authentication → Principals** → create a real admin user with a new password. Optionally delete/rotate the bootstrap `admin` fallback afterwards.
2. **Configuration → Outbound → DKIM** → click "Create signature":
   - Domain: `<your-domain>`
   - Selector: `mail`
   - Algorithm: RSA-SHA256
   - Key size: **1024 bits** (Hetzner Cloud DNS won't accept TXT records over 255 bytes — 2048-bit DKIM public keys exceed this; 1024-bit is still universally accepted)
   - Canonicalization: relaxed/relaxed
   - Save & generate key
3. **Configuration → Outbound → DKIM → mail selector** → copy the public-key TXT value shown in the "DNS Record" section.

### 4. Publish the DKIM record

Paste the DKIM public key into `terraform/terraform.tfvars.json`:

```bash
# Build the TXT record value (Hetzner wants single-line, outer-quoted form)
PUBLIC_KEY='...paste from Stalwart admin UI...'
jq --arg v "\"v=DKIM1; k=rsa; p=${PUBLIC_KEY}\"" \
  '.dkim_public_value = $v' \
  terraform/terraform.tfvars.json > /tmp/t && mv /tmp/t terraform/terraform.tfvars.json

# Sanity-check the total length is ≤ ~255 bytes
jq -r .dkim_public_value terraform/terraform.tfvars.json | wc -c

# Publish
nix run .#tf-apply
```

Verify:

```bash
dig TXT mail._domainkey.<domain> @hydrogen.ns.hetzner.com +short
# expect: "v=DKIM1; k=rsa; p=MIG..."
```

### 5. Create role mailboxes

In the admin UI → **Management → Accounts → New**:

- `noreply@<domain>` — used by brygge for outbound transactional mail (magic links, notifications). Generate a strong password and save it for the env file step below.
- `treasurer@<domain>`, `secretary@<domain>`, `admin@<domain>`, etc. — shared role mailboxes for board members. Password per role.

Aliases: each mailbox can have multiple aliases configured in the same screen (e.g. `kasserer@<domain>` → treasurer).

### 6. Wire brygge to SMTP

Edit `/etc/brygge/env` on the server to add SMTP credentials:

```
SMTP_HOST=localhost
SMTP_PORT=587
SMTP_USERNAME=noreply@<domain>
SMTP_PASSWORD=<password from step 5>
EMAIL_FROM=noreply@<domain>
```

Restart brygge:

```bash
ssh root@<server-ip> 'systemctl restart brygge'
```

Test by requesting a magic link against `https://<domain>/api/v1/auth/magic-link`. The email should arrive in your inbox within ~30s.

### 7. First Bulwark login

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
| Stalwart admin settings (DKIM, spam, TLS) | Stalwart RocksDB | Admin UI at `https://mail.<domain>/` |
| Mailbox accounts + passwords | Stalwart RocksDB | Admin UI |
| Mail contents | Stalwart RocksDB | IMAP / JMAP clients |
| Bootstrap admin password | `/etc/stalwart/admin-password` on the server | `install` + `systemctl restart stalwart` |
| brygge's SMTP credentials | `/etc/brygge/env` on the server | Edit + `systemctl restart brygge` |

---

## Related

- [docs/deploy.md](../deploy.md) — overall deploy guide
- [DIL-141](https://linear.app/dillonteknisk/issue/DIL-141) — parent feature (self-host mail)
- [DIL-166](https://linear.app/dillonteknisk/issue/DIL-166) — first-deploy workflow simplification
