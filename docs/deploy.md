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
- A **Hetzner Cloud** account with a project
- A **domain name** you control the nameservers for
- An **SSH key** — the public key goes into `nix/configuration.local.nix`

---

## Initial deployment

### 1. Configure the club

One file drives everything — infra + NixOS — so per-club config lives in one place: `terraform/terraform.tfvars.json`. It is **tracked in git with placeholder values** so the flake can read it in pure evaluation mode. Edit it with your real values, then tell git to ignore your local changes:

```bash
$EDITOR terraform/terraform.tfvars.json

# Mark the file skip-worktree — git will ignore your edits forever
git update-index --skip-worktree terraform/terraform.tfvars.json
```

After `skip-worktree`:

- `git status`, `git diff`, `git commit -a` all ignore your changes.
- The flake (via `builtins.readFile`) still sees your values because Nix reads the working tree, not HEAD.
- A pre-commit hook (auto-installed by the dev shell) blocks accidental commits of this file as a second line of defence.

To pull upstream changes to the placeholder in the future:

```bash
git update-index --no-skip-worktree terraform/terraform.tfvars.json
git pull
$EDITOR terraform/terraform.tfvars.json   # re-apply your values
git update-index --skip-worktree terraform/terraform.tfvars.json
```

Required values:

| Key | Used by | Purpose |
|-----|---------|---------|
| `hcloud_token` | terraform | Hetzner Cloud + DNS API |
| `admin_email` | terraform, NixOS | ACME/Let's Encrypt contact |
| `admin_ssh_keys` | terraform, NixOS | Root SSH access and rescue-mode bootstrap |
| `hetzner_s3_*`, `s3_bucket`, `s3_endpoint` | terraform | State backend |
| `domain` | terraform, NixOS | Primary domain for the club |
| `server_name` | terraform, NixOS | Hetzner server name + NixOS hostname |
| `server_type`, `location`, `timezone` | terraform, NixOS | Server size, region, TZ |
| `resend_*` | terraform | Optional email DNS records |

`terraform.tfvars.json` is the **single source of truth**. The flake reads it via `builtins.fromJSON` to populate the NixOS host config, so nothing club-specific is hardcoded in `nix/`.

### 2. Provision infrastructure

```bash
nix run .#tf-plan    # preview
nix run .#tf-apply   # creates server, firewall, DNS zone + records
```

After the first apply, switch your registrar's nameservers to Hetzner's:

- `hydrogen.ns.hetzner.com`
- `oxygen.ns.hetzner.com`
- `helium.ns.hetzner.de`

DNS propagation typically takes a few minutes; Let's Encrypt cert issuance depends on this.

### 3. Boot the server into rescue mode

nixos-anywhere kexecs into the NixOS installer from Hetzner's Debian-based rescue system:

```bash
hcloud server enable-rescue brygge --type linux64 --ssh-key <your-key-id>
hcloud server reset brygge
```

Wait ~30 seconds for the server to come back up in rescue. Confirm you can SSH:

```bash
ssh root@<server-ip>   # rescue mode fingerprint; accept it
```

### 4. Install NixOS

```bash
nix run .#install -- <server-ip>
```

This:
1. Builds `nixosConfigurations.brygge` locally
2. Uses nixos-anywhere to kexec into the installer
3. Runs disko to partition `/dev/sda` (GPT + BIOS boot + ext4 root)
4. Copies the system closure to the new root and activates it
5. Reboots into NixOS

First install takes 5–10 minutes.

### 5. Install secrets

The `services.brygge` systemd unit reads a root-owned EnvironmentFile at `/etc/brygge/env`. Create it on the server:

```bash
ssh root@<server-ip>
install -m 0400 -o root -g root /dev/stdin /etc/brygge/env <<'EOF'
JWT_SECRET=...
VIPPS_CLIENT_ID=...
VIPPS_CLIENT_SECRET=...
VIPPS_SUBSCRIPTION_KEY=...
VIPPS_MSN=...
RESEND_API_KEY=...
S3_ENDPOINT=https://nbg1.your-objectstorage.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=...
S3_SECRET_KEY=...
S3_REGION=nbg1
EOF
systemctl restart brygge
```

See `deploy/.env.example` for the full list of keys Brygge reads.

### 6. Verify

```bash
curl -I https://klubb.no                     # → 200 from Brygge
curl -I https://matrix.klubb.no/_matrix/...  # → Dendrite
curl -I https://element.klubb.no             # → Element static
curl -I https://status.klubb.no              # → Uptime Kuma
```

### 7. Seed demo data (optional)

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

Or from the server: `nixos-rebuild switch --rollback`.

---

## DNS records managed by terraform

All point to the brygge server's IPv4 (plus Resend TXT/MX when configured):

| Type | Name       | Purpose              |
|------|------------|----------------------|
| A    | `@`        | Main site, API       |
| A    | `matrix`   | Dendrite (Matrix)    |
| A    | `element`  | Element Web          |
| A    | `status`   | Uptime Kuma          |
| TXT  | `resend._domainkey` | Resend DKIM (optional) |
| TXT  | `send`              | Resend SPF (optional)  |
| MX   | `send`              | Resend MX (optional)   |
| TXT  | `_dmarc`            | Resend DMARC (optional)|

---

## Services on the VM

| Systemd unit          | Port             | Purpose                     |
|-----------------------|------------------|-----------------------------|
| `caddy.service`       | 80, 443          | Reverse proxy + Let's Encrypt |
| `brygge.service`      | 8080 (loopback)  | Go API + embedded Vue SPA   |
| `brygge-migrate.service` | —             | Oneshot, runs before brygge |
| `postgresql.service`  | unix socket      | brygge + dendrite databases |
| `redis-brygge.service`| unix socket      | Cache, sessions, rate limit |
| `dendrite.service`    | 8008 (loopback)  | Matrix homeserver           |
| `uptime-kuma.service` | 3001 (loopback)  | Status page                 |

Caddy provisions TLS automatically for each virtualhost using the ACME email in `configuration.local.nix`.

---

## Operations

### Logs

```bash
ssh brygge journalctl -u brygge -f
ssh brygge journalctl -u caddy -f
```

### Database

```bash
ssh brygge 'sudo -u brygge psql brygge'
```

### Backups

The server itself is disposable — rebuild it from the flake at any time. Data that needs real backups:

- `/var/lib/postgresql` — both `brygge` and `dendrite` databases
- `/var/lib/dendrite` — matrix signing keys
- `/var/lib/uptime-kuma` — monitor config and history

Enable **Hetzner Snapshots** (20% of server cost, daily automatic) for filesystem-level protection, and run pg_dump on a cron for logical backups:

```bash
ssh brygge 'sudo -u postgres pg_dumpall | gzip' > backup-$(date +%Y%m%d).sql.gz
```

### Rebuild on a new server

If the VM is destroyed, `nix run .#tf-apply` provisions a new one and `nix run .#install -- <new-ip>` re-lays the exact same system. Restore Postgres from the logical backup.

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

See [troubleshooting.md](troubleshooting.md) for dirty migrations, TLS issues, and deploy-rs rollback scenarios.

---

## Scaling

The API is stateless and supports horizontal scaling. For multi-node deployments, see the [Kubernetes migration guide](k8s.md). For a single larger VM, change `server_type` in `terraform.tfvars` to `cx32` (4 vCPU / 8 GB) and redeploy — the NixOS config is portable across Hetzner x86 instance sizes.
