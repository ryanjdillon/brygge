## Deployment Guide

Brygge is deployed to a single Hetzner Cloud VM running **NixOS**, provisioned declaratively with **Terranix** (infrastructure) and **nixos-anywhere** + **deploy-rs** (operating system). There are no container images or long-running shell sessions — every change to the server goes through the flake.

For the non-developer club administrator walkthrough, see [setup.md](setup.md).

---

## How it fits together

```
  ┌────────────────────────┐   tf-apply    ┌──────────────────────┐
  │ terraform/ (Terranix)  │ ────────────▶ │  Hetzner Cloud VM    │
  │  server, firewall, DNS │               │  (CX23, Debian boot) │
  └────────────────────────┘               └───────────┬──────────┘
                                                       │ rescue mode
                                              nix run .#install
                                                       │
                                                       ▼
                                           ┌─────────────────────┐
                                           │  NixOS (disko, GPT) │
                                           └───────────┬─────────┘
                                                       │ subsequent deploys
                                              nix run .#deploy
                                                       │
                                                       ▼
                                           ┌─────────────────────────────┐
                                           │ services.brygge (systemd)   │
                                           │ postgres • redis • caddy    │
                                           │ dendrite • element • kuma   │
                                           └─────────────────────────────┘
```

---

## Prerequisites

- A workstation with **Nix** (flakes + `nix-command` enabled)
- A **Hetzner Cloud** account with a project and the `hcloud` CLI authenticated (`hcloud context create ...`)
- A **domain name** you control the nameservers for
- At least one SSH keypair for admin access (e.g. `ssh-keygen -t ed25519 -f ~/.ssh/brygge_id_ed25519`)

---

## Initial deployment

### 1. Configure the club

All per-club config lives in **one file**: `terraform/terraform.tfvars.json`. Both terraform and the NixOS flake read it — no values are hardcoded in `nix/` or duplicated anywhere.

The file is tracked in git with placeholder values so the flake always evaluates (pure eval, no `--impure` needed). Edit it with your real values:

```bash
$EDITOR terraform/terraform.tfvars.json
```

Required values:

| Key | Used by | Purpose |
|-----|---------|---------|
| `hcloud_token` | terraform | Hetzner Cloud + DNS API token (create at console.hetzner.cloud → Security → API Tokens, **Read & Write**) |
| `admin_email` | NixOS (Caddy ACME) | **Must be a real, deliverable address.** Let's Encrypt rejects `.example` / `.invalid` TLDs. |
| `admin_ssh_keys` | terraform, NixOS | JSON array of public keys. Mirrored to the Hetzner project for rescue-mode boot and to `root`'s `authorized_keys` on the installed system. |
| `hetzner_s3_*`, `s3_bucket`, `s3_endpoint` | terraform | Object Storage for Terraform state. Create a bucket + S3 credentials under console.hetzner.cloud → Security → Object Storage. |
| `domain` | terraform, NixOS | Primary domain for the club (e.g. `klubb.no`) |
| `server_name` | terraform, NixOS | Hetzner server name + NixOS hostname |
| `server_type`, `location`, `timezone` | terraform, NixOS | Defaults: `cx23`, `nbg1`, `Europe/Oslo` |
| `dkim_public_value`, `dmarc_policy` | terraform | Mail DNS records (see [mail/setup.md](mail/setup.md)) |

**Protection against committing secrets**: a pre-commit hook at `.githooks/pre-commit` rejects any attempt to stage `terraform/terraform.tfvars.json`. The dev shell's `shellHook` auto-installs it by setting `core.hooksPath` the first time you `nix develop`. If you accept the hook, `git status` will still show the file as modified, but `git commit` (with or without `-a`) will fail:

```
ERROR: refusing to commit terraform/terraform.tfvars.json
```

Do not use `git update-index --skip-worktree` on this file — it's incompatible with Nix flakes (Nix reads the git HEAD version when skip-worktree is set, so your local edits disappear).

### 2. Provision infrastructure

```bash
nix develop             # enter dev shell (installs the pre-commit hook)
nix run .#tf-plan       # preview
nix run .#tf-apply      # creates server, firewall, DNS zone + records
```

After the first apply, switch your registrar's nameservers to Hetzner's:

- `hydrogen.ns.hetzner.com`
- `oxygen.ns.hetzner.com`
- `helium.ns.hetzner.de`

DNS propagation typically takes a few minutes. Verify:

```bash
dig +short A klubb.no                 # should return the Hetzner IP
hcloud server list                    # confirm "brygge" is running
tofu -chdir=terraform output server_ipv4
```

### 3. Boot the server into rescue mode

nixos-anywhere kexecs into the NixOS installer from Hetzner's Debian-based rescue system. First find the SSH key id that terraform uploaded:

```bash
hcloud ssh-key list
```

Then enable rescue with that key attached, and reboot:

```bash
hcloud server enable-rescue brygge --type linux64 --ssh-key <id-or-name>
hcloud server reset brygge
```

Wait ~30 seconds for rescue to come up. Clear any stale host key and connect once to accept the rescue fingerprint:

```bash
ssh-keygen -R <server-ip>
ssh -o StrictHostKeyChecking=accept-new root@<server-ip> uname -a
```

### 4. Install NixOS

```bash
nix run .#install -- <server-ip>
```

This:
1. Builds `nixosConfigurations.brygge.config.system.build.toplevel` + `diskoScript` locally
2. Passes both store paths to nixos-anywhere (`--store-paths`)
3. nixos-anywhere kexecs into the installer, runs disko to partition `/dev/sda` (GPT + EF02 BIOS-boot + ext4 root), copies the closure, installs GRUB (BIOS, not EFI — Hetzner x86 VMs boot legacy), activates, reboots

First install takes 5–10 minutes. Watch serial console via `hcloud server request-console brygge` if it stalls.

### 5. Configure SSH for the installed server

After reboot, the server has fresh host keys. Clear the rescue-mode key and set up a persistent entry:

```bash
ssh-keygen -R <server-ip>
```

Add to `~/.ssh/config`:

```
Host <server-ip> <domain> brygge
    User root
    IdentityFile ~/.ssh/brygge_id_ed25519
    IdentitiesOnly yes
```

`IdentitiesOnly yes` is critical — it prevents your SSH agent from blasting every loaded key at the server, which can trip `fail2ban` (5 failed keys = 10-minute ban).

### 6. Install secrets

The `brygge` and `brygge-migrate` systemd units read a root-owned EnvironmentFile at `/etc/brygge/env`. Create it on the server:

```bash
JWT_SECRET=$(openssl rand -base64 48)
ssh root@<server-ip> 'install -m 0400 -o root -g root /dev/stdin /etc/brygge/env' <<EOF
JWT_SECRET=${JWT_SECRET}

# Database and cache — NixOS uses Unix sockets (not docker hostnames)
DATABASE_URL=postgres:///brygge?host=/run/postgresql&sslmode=disable
REDIS_URL=unix:///run/redis-brygge/redis.sock

# Matrix (brygge talks to dendrite on loopback; public URL is matrix.<domain>)
MATRIX_HOMESERVER_URL=http://127.0.0.1:8008

# Vipps (client ID/secret, MSN, subscription key — from vippsmobilepay.com)
VIPPS_CLIENT_ID=
VIPPS_CLIENT_SECRET=
VIPPS_SUBSCRIPTION_KEY=
VIPPS_MSN=

# Mail (self-hosted Stalwart; see docs/developer/mail/setup.md).
# The relay@ principal is provisioned declaratively from
# /etc/stalwart/relay-password by the stalwart-relay-account
# systemd unit; SMTP_PASSWORD here must match that file.
SMTP_HOST=mail.<domain>
SMTP_PORT=465
SMTP_USERNAME=relay
SMTP_PASSWORD=
EMAIL_FROM=<Club Name> <relay@<domain>>
EMAIL_REPLY_TO=post@<domain>

# S3-compatible object storage (documents, chart tiles, etc.)
S3_ENDPOINT=https://nbg1.your-objectstorage.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=
S3_SECRET_KEY=
S3_REGION=nbg1
EOF
```

Critical notes on the env file format:

- **systemd's parser is stricter than docker-compose's.** No `export` prefix, no inline `#` comments on value lines, quoted values only with `"..."` (not `'...'`).
- **Unix sockets for DB/Redis.** If you copy an old docker-compose `.env`, replace `DATABASE_URL=postgres://brygge:brygge@db:5432/...` with the socket form above. The module does NOT default this — systemd's `EnvironmentFile` overrides any `Environment=` directive set in Nix.
- **Do not set `DOMAIN`** in this file. The Nix module passes it in from `clubConfig.domain`; the EnvironmentFile would override it.

Kick off the services:

```bash
ssh root@<server-ip> 'systemctl restart brygge-migrate brygge'
ssh root@<server-ip> 'systemctl --failed'    # should be empty
```

### 7. Verify

```bash
# Watch Caddy obtain Let's Encrypt certs (first HTTPS hit triggers it)
curl -sI https://klubb.no/api/health
ssh root@<server-ip> 'journalctl -u caddy -f'
# look for "certificate obtained successfully" for each vhost
```

### 8. Set up the mail server

Follow [mail/setup.md](mail/setup.md) for:

- Stalwart admin bootstrap (first password, DKIM generation)
- DNS DKIM record publishing
- Setting the `relay@<domain>` password (auto-provisioned via `stalwart-relay-account` systemd unit) + creating role mailboxes for board members
- Bulwark webmail first login

Once `SMTP_HOST`/`SMTP_PASSWORD` are filled in `/etc/brygge/env` and `brygge.service` is restarted, magic-link login via self-hosted mail works end-to-end.

All four should respond 200 within ~60 seconds:

```bash
curl -sI https://klubb.no/api/health
curl -sI https://matrix.klubb.no/_matrix/client/versions
curl -sI https://element.klubb.no
curl -sI https://status.klubb.no
```

### 8. Seed demo data (optional)

```bash
ssh root@<server-ip> 'sudo -u brygge brygge-seed'
```

---

## Updating the server

Every change — app code, service config, kernel upgrade — goes through one command:

```bash
nix run .#deploy -- <server-ip-or-hostname>
```

Under the hood, deploy-rs:
- builds the new system closure on your workstation
- copies the closure to the server
- activates it with a health check and an automatic rollback window

If activation fails the server rolls back on its own. To manually undo:

```bash
nix run .#deploy -- <host> --rollback
```

Or directly on the server: `nixos-rebuild switch --rollback`.

### When only the env file changed

Editing `/etc/brygge/env` doesn't trigger a rebuild; restart affected units:

```bash
ssh root@<host> 'systemctl restart brygge-migrate brygge'
```

### When the ACME email changed

Caddy caches the ACME account registration on disk; changing `admin_email` in tfvars + redeploying leaves the old failed account in place. Wipe it:

```bash
ssh root@<host> 'systemctl stop caddy && rm -rf /var/lib/caddy/.local/share/caddy && systemctl start caddy'
```

---

## DNS records managed by terraform

All point to the brygge server's IPv4:

| Type  | Name                 | Purpose                                |
|-------|----------------------|----------------------------------------|
| A     | `@`                  | Main site, API                         |
| A     | `matrix`             | Dendrite (Matrix)                      |
| A     | `element`            | Element Web                            |
| A     | `status`             | Uptime Kuma                            |
| A     | `mail`               | Stalwart (SMTP/IMAP/JMAP)              |
| A     | `webmail`            | Bulwark webmail                        |
| MX    | `@` → mail           | Inbound mail to the server             |
| TXT   | `@`                  | SPF                                    |
| TXT   | `_dmarc`             | DMARC policy                           |
| TXT   | `mail._domainkey`    | DKIM public key                        |
| CNAME | `autoconfig`         | Thunderbird autoconfig                 |
| SRV   | `_imaps._tcp`        | IMAPS service record                   |
| SRV   | `_submission._tcp`   | SMTP submission service record         |

The server also has a PTR (rDNS) record pointing at `mail.<domain>`, managed via `hcloud_rdns` in `terraform/server.nix`.

Mail-specific DNS setup is covered in [mail/setup.md](mail/setup.md).

---

## Services on the VM

| Systemd unit          | Port             | Purpose                     |
|-----------------------|------------------|-----------------------------|
| `caddy.service`       | 80, 443          | Reverse proxy + Let's Encrypt ACME |
| `brygge.service`      | 8080 (loopback)  | Go API + embedded Vue SPA   |
| `brygge-migrate.service` | —             | Oneshot, runs before brygge |
| `postgresql.service`  | unix socket      | brygge + dendrite databases (peer auth) |
| `redis-brygge.service`| unix socket      | Cache, sessions, rate limit |
| `dendrite.service`    | 8008 (loopback)  | Matrix homeserver           |
| `uptime-kuma.service` | 3001 (loopback)  | Status page                 |
| `stalwart.service`    | 25, 465, 587, 993 public; 8088 loopback | Stalwart mail server (SMTP/IMAP/JMAP) |
| `podman-bulwark.service` | 3000 (loopback) | Bulwark webmail (JMAP client) |
| `acme-mail.<domain>.service` | — (systemd timer) | Renews mail cert via Let's Encrypt HTTP-01 |
| `fail2ban.service`    | —                | SSH brute-force protection  |

Caddy is the sole internet-facing HTTP service and terminates TLS for each HTTPS virtualhost using the ACME email from `clubConfig.adminEmail` (→ `admin_email` in tfvars). Stalwart terminates TLS itself for SMTPS/IMAPS, reading the same Let's Encrypt cert files that security.acme issues for `mail.<domain>`.

For mail-specific configuration, admin bootstrap, DKIM, and troubleshooting, see [mail/setup.md](mail/setup.md).

---

## Operations

### Logs

```bash
ssh root@<host> journalctl -u brygge -f
ssh root@<host> journalctl -u caddy -f
ssh root@<host> journalctl -u brygge-migrate --no-pager
```

### Database

```bash
ssh root@<host> 'sudo -u brygge psql brygge'
ssh root@<host> 'sudo -u postgres psql -c "\l"'   # list all DBs
```

Peer-authenticated Unix socket, no password.

### Backups

The server itself is disposable — rebuild it from the flake at any time. Data that needs real backups:

- `/var/lib/postgresql` — both `brygge` and `dendrite` databases
- `/var/lib/dendrite` — matrix signing keys (`matrix_key.pem`)
- `/var/lib/uptime-kuma` — monitor config and history
- `/etc/brygge/env` — secrets file (not declared in Nix)

Enable **Hetzner Snapshots** (20% of server cost, daily automatic) for filesystem-level protection, and run pg_dump on a cron for logical backups:

```bash
ssh root@<host> 'sudo -u postgres pg_dumpall | gzip' > backup-$(date +%Y%m%d).sql.gz
```

### Rebuild on a new server

If the VM is destroyed, provision a replacement:

```bash
nix run .#tf-apply                          # creates new VM + updates DNS
hcloud server enable-rescue brygge --type linux64 --ssh-key <id>
hcloud server reset brygge
nix run .#install -- <new-ip>
# re-install /etc/brygge/env
zcat backup-YYYYMMDD.sql.gz | ssh root@<new-ip> 'sudo -u postgres psql'
```

---

## Cost

| Resource                           | Monthly (EUR) |
|------------------------------------|---------------|
| CX23 (x86_64, 2 vCPU, 4 GB, 40 GB) | 4.59          |
| IPv4 address                       | 0.50          |
| Hetzner snapshots (20%)            | 0.92          |
| Object Storage (10 GB docs)        | 0.52          |
| Hetzner DNS                        | free          |
| **Total**                          | **~6.53**     |

---

## Troubleshooting

### `brygge-migrate.service: Failed to load environment files`

`/etc/brygge/env` is missing. Create it (see step 6).

### `brygge-migrate-start[...]: error: failed to open database: dial tcp: lookup db`

Your env file has a `DATABASE_URL` that references a docker-compose hostname. Replace with the Unix socket form:

```
DATABASE_URL=postgres:///brygge?host=/run/postgresql&sslmode=disable
```

### Caddy: `contact email has invalid domain`

`admin_email` in tfvars is still the placeholder. Update it to a real, deliverable address (Let's Encrypt rejects `.example` / `.invalid` / `.local` TLDs), redeploy, and wipe Caddy's cached account (see "When the ACME email changed" above).

### `ssh: Permission denied (publickey)` after `.#install`

Your SSH client is offering the wrong key. Either:
- Set `IdentityFile` in `~/.ssh/config` (recommended — see step 5), or
- `ssh-add ~/.ssh/brygge_id_ed25519` to load it in your agent

And confirm the public key in `admin_ssh_keys` (tfvars) matches what you're offering:

```bash
jq .admin_ssh_keys terraform/terraform.tfvars.json
ssh-keygen -lf ~/.ssh/brygge_id_ed25519.pub
```

Three bad key offers in a row will trip `fail2ban` (10-minute ban). Use `IdentitiesOnly yes`.

### Kernel boots but no login prompt (console shows "booting the kernel" and freezes)

Missing virtio drivers in the initrd. The flake's `nix/host.nix` already declares them:

```nix
boot.initrd.availableKernelModules = [ "virtio_pci" "virtio_scsi" "virtio_blk" ... ];
```

If you see this on a new install, re-run `nix run .#install` — an older closure without these modules may have been deployed.

### `go-migrate` panics on startup: `failed to parse CA certificate`

The flake overlays `pkgs.go-migrate` with `tags = [ "postgres" ]` to strip out the snowflake driver, whose init function panics. If you see this, verify the overlay is in effect:

```bash
nix eval --raw .#nixosConfigurations.brygge.pkgs.go-migrate
strings $(...) | grep snowflake    # should be empty
```

### Changes to tfvars aren't picked up by Nix

If `nix eval .#nixosConfigurations.brygge.config.services.brygge.domain` returns the placeholder, something is hiding your edits from the flake:

1. `git ls-files -v terraform/terraform.tfvars.json` — if it shows `S` (skip-worktree), run `git update-index --no-skip-worktree terraform/terraform.tfvars.json`. Nix reads the git HEAD version, not your disk, when skip-worktree is set.
2. `git status` should show the file as modified.
3. `jq .domain terraform/terraform.tfvars.json` should show your real value.

---

## Scaling

The API is stateless and supports horizontal scaling. For multi-node deployments, see the [Kubernetes migration guide](k8s.md). For a single larger VM, change `server_type` in `terraform.tfvars.json` to `cx33` (4 vCPU / 8 GB) or higher and `nix run .#tf-apply` — the NixOS config is portable across Hetzner x86 instance sizes. The server is destroyed and recreated on architecture changes, so restore from pg_dump afterward.
