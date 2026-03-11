# Deployment Guide

This guide covers deploying Brygge to production. For the full initial setup walkthrough (aimed at non-developer club administrators), see [setup.md](setup.md).

---

## Prerequisites

- A **VPS** or cloud server running **Ubuntu 24.04 LTS** (ARM64 or x86_64)
- A **domain name** with DNS access
- **Docker** with the Compose plugin
- **SSH access** to your server

### DNS Records

Point the following A records to your server's IP address:

| Type | Name       | Value         |
|------|------      |-------        |
| A    | `@` (root) | `<server IP>` |
| A    | `matrix`   | `<server IP>` |
| A    | `element`  | `<server IP>` |
| A    | `status`   | `<server IP>` |

---

## Deployment Steps

### 1. Prepare the Server

```bash
# Install Docker
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
# Log out and back in
```

### 2. Clone and Configure

```bash
cd /opt
sudo git clone https://github.com/brygge-klubb/brygge.git
sudo chown -R $USER:$USER brygge
cd brygge

# Copy and edit environment configuration
cp deploy/.env.example deploy/.env
nano deploy/.env
```

Generate secrets:

```bash
# Database password
openssl rand -base64 32

# JWT secret
openssl rand -base64 48
```

See [deploy/.env.example](../deploy/.env.example) for all configuration options with inline documentation.

### 3. Initial Setup

```bash
chmod +x scripts/brygge.sh
./scripts/brygge.sh setup
```

This pulls Docker images, starts the database and Redis, runs migrations, and starts all services. Traefik automatically provisions TLS certificates from Let's Encrypt.

### 4. Verify

```bash
./scripts/brygge.sh status
```

All containers should show "Up" or "healthy". Visit:

- `https://your-domain.com` — main site
- `https://status.your-domain.com` — Uptime Kuma status page
- `https://element.your-domain.com` — Matrix client

### 5. Configure Vipps Login

1. Go to the [Vipps developer portal](https://developer.vippsmobilepay.com/)
2. Create an application and set the redirect URI to `https://your-domain.com/api/auth/vipps/callback`
3. Enter the Client ID, Client Secret, Subscription Key, and Merchant Serial Number in `deploy/.env`
4. Restart: `./scripts/brygge.sh update`

---

## Updating

```bash
cd /opt/brygge
git pull
./scripts/brygge.sh update
```

This pulls the latest Docker images, runs any new migrations, and restarts services. Downtime is typically a few seconds.

### Rollback

If something goes wrong, roll back to a specific image SHA:

```bash
IMAGE=ghcr.io/brygge-klubb/brygge:<sha> docker compose -f deploy/docker-compose.yml up -d api
```

---

## Backups

### Database

The `brygge backup` command creates a compressed database dump:

```bash
./scripts/brygge.sh backup
```

Set up a daily cron job:

```bash
crontab -e
# Add:
0 2 * * * /opt/brygge/scripts/brygge.sh backup >> /var/log/brygge-backup.log 2>&1
```

Backups are stored in the `backups/` directory. Old backups (30+ days) are automatically removed.

### Restore

```bash
gunzip -c backups/brygge_20260306_020000.sql.gz | \
  docker compose -f deploy/docker-compose.yml exec -T db \
  psql -U brygge brygge
```

---

## Services Overview

| Service         | Image                            | Purpose                        | Subdomain   |
|---------        |-------                           |---------                       |-----------  |
| **api**         | `ghcr.io/brygge-klubb/brygge`    | Go API + embedded SPA          | `@`         |
| **db**          | `postgres:16-alpine`             | Primary database               | internal    |
| **redis**       | `redis:7-alpine`                 | Cache, sessions, rate limiting | internal    |
| **traefik**     | `traefik:v3.3`                   | Reverse proxy, TLS             | internal    |
| **dendrite**    | `matrixdotorg/dendrite-monolith` | Matrix homeserver (forum)      | `matrix.*`  |
| **element**     | `vectorim/element-web`           | Matrix web client              | `element.*` |
| **uptime-kuma** | `louislam/uptime-kuma:1`         | Status page                    | `status.*`  |
| **migrate**     | `migrate/migrate:v4`             | Database migrations (one-shot) | internal    |

---

## Resource Requirements

| Size                      | Users        | Recommended Server            |
|------                     |-------       |--------------------           |
| Small club (<100 members) | Low traffic  | 2 vCPU, 4 GB RAM, 40 GB disk  |
| Medium club (100-500)     | Moderate     | 4 vCPU, 8 GB RAM, 80 GB disk  |
| Large club (500+)         | High traffic | Consider [Kubernetes](k8s.md) |

---

## Monitoring

Brygge includes Uptime Kuma for status monitoring at `status.your-domain.com`. The API exposes a health endpoint:

```bash
curl https://your-domain.com/api/health
```

Returns service status for PostgreSQL and Redis with HTTP 200 (healthy) or 503 (degraded).

---

## Troubleshooting

See the [troubleshooting section in setup.md](setup.md#10-troubleshooting) for common issues.

---

## Providers

Provider-specific deployment guides with optimized configurations.

<details>
<summary><h3>Hetzner Cloud</h3></summary>

Hetzner Cloud is the recommended hosting provider. It offers affordable ARM64 servers in European data centers with excellent performance.

#### 1. Create a Server

1. Sign up at [hetzner.com/cloud](https://www.hetzner.com/cloud/)
2. Create a new project
3. Add your SSH public key under **Security > SSH Keys**
4. Create a server:
   - **Location**: Falkenstein (fsn1), Nuremberg (nbg1), or Helsinki (hel1)
   - **Image**: Ubuntu 24.04
   - **Type**: **CAX11** (ARM64, 2 vCPU, 4 GB RAM, 40 GB disk) — ~3.29 EUR/month
   - **SSH key**: Select the key you added
   - **Name**: e.g. `brygge-prod`

#### 2. Set Up Object Storage

For document uploads and harbour charts:

1. Go to **Object Storage** in the Hetzner Cloud console
2. Create a bucket (e.g. `brygge-documents`) in the same region as your server
3. Generate S3 credentials under **Security > API Tokens > Object Storage**
4. Add to `deploy/.env`:

```env
S3_ENDPOINT=https://fsn1.your-objectstorage.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=<your access key>
S3_SECRET_KEY=<your secret key>
S3_REGION=fsn1
```

#### 3. Configure Firewall

In the Hetzner Cloud console under **Firewalls**:

| Direction | Protocol | Port | Source                      |
|-----------|----------|------|--------                     |
| Inbound   | TCP      | 22   | Your IP (SSH)               |
| Inbound   | TCP      | 80   | Any (HTTP → HTTPS redirect) |
| Inbound   | TCP      | 443  | Any (HTTPS)                 |

Apply the firewall to your server.

#### 4. Configure DNS

If using Hetzner DNS (or your domain registrar):

```
A    @        <server IPv4>
A    matrix   <server IPv4>
A    element  <server IPv4>
A    status   <server IPv4>
AAAA @        <server IPv6>    (optional)
```

#### 5. Deploy

SSH into your server and follow the [deployment steps](#deployment-steps) above.

```bash
ssh root@<server-ip>
```

#### 6. Automatic Backups (Hetzner)

Enable **Hetzner Snapshots** for full server backup (20% of server cost):

1. Go to your server in the Hetzner Cloud console
2. Enable **Backups** (automatic daily snapshots, 7-day retention)

For database-level backups, use the [cron backup](#backups) in addition to snapshots.

#### Cost Estimate

| Resource                           | Monthly Cost  |
|----------                          |-------------  |
| CAX11 server (ARM64, 2 vCPU, 4 GB) | ~3.29 EUR     |
| Snapshots (20% of server)          | ~0.66 EUR     |
| Object Storage (10 GB)             | ~0.52 EUR     |
| **Total**                          | **~4.47 EUR** |

</details>

<details>
<summary><h3>DigitalOcean</h3></summary>

#### Server Setup

1. Create a Droplet:
   - **Image**: Ubuntu 24.04
   - **Plan**: Basic, Regular (AMD), $6/month (1 vCPU, 1 GB RAM) or $12/month (2 vCPU, 2 GB RAM)
   - **Region**: Choose closest to your users
   - **Authentication**: SSH key

2. Point your domain's DNS to the Droplet IP
3. SSH in and follow the [deployment steps](#deployment-steps)

#### Object Storage

Use DigitalOcean Spaces for document storage:

```env
S3_ENDPOINT=https://ams3.digitaloceanspaces.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=<spaces access key>
S3_SECRET_KEY=<spaces secret key>
S3_REGION=ams3
```

</details>

<details>
<summary><h3>Generic VPS</h3></summary>

Brygge runs on any VPS provider that supports Docker. Requirements:

- **OS**: Ubuntu 24.04 LTS (other Debian-based distros work too)
- **Architecture**: ARM64 or x86_64 (rebuild the Docker image for x86_64, see below)
- **Minimum**: 1 vCPU, 2 GB RAM, 20 GB disk
- **Recommended**: 2 vCPU, 4 GB RAM, 40 GB disk

#### Building for x86_64

The default Dockerfile targets ARM64. To build for x86_64, change the build args:

```bash
# In the Dockerfile, change:
# GOARCH=arm64  →  GOARCH=amd64

docker build --platform linux/amd64 -t brygge .
```

Or use Docker Buildx for multi-platform:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t brygge .
```

#### S3-Compatible Storage

Any S3-compatible provider works (MinIO, Wasabi, Backblaze B2, Cloudflare R2):

```env
S3_ENDPOINT=https://s3.your-provider.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=<key>
S3_SECRET_KEY=<secret>
S3_REGION=auto
```

</details>

---

## Scaling

For deployments beyond a single VPS, see the [Kubernetes migration guide](k8s.md). The API is stateless and supports horizontal scaling behind a load balancer.
