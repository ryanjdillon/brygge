# Brygge -- Deployment Guide

This guide walks you through deploying Brygge on a fresh server. It is written for club board members who can follow instructions but may not have a software development background. If you get stuck, open an issue on the project repository.

---

## 1. Prerequisites

You will need:

- A **VPS** (virtual private server) running **Ubuntu 24.04 LTS**. We recommend [Hetzner CAX11](https://www.hetzner.com/cloud/) -- ARM64, 2 vCPU, 4 GB RAM, approximately 3.29 EUR/month.
- A **domain name** (e.g. `mitt-klubb.no`) with the ability to edit DNS records.
- **SSH access** to your server.

### DNS records

Point the following DNS A records to your server's IP address before you begin. Replace `203.0.113.10` with your server's actual IP.

| Type | Name               | Value         |
|------|--------------------|---------------|
| A    | `@` (or root)      | 203.0.113.10  |
| A    | `matrix`           | 203.0.113.10  |
| A    | `element`          | 203.0.113.10  |
| A    | `status`           | 203.0.113.10  |

DNS changes can take up to 24 hours to propagate, though most providers update within minutes.

---

## 2. Install Docker

SSH into your server and install Docker Engine with the Compose plugin.

```bash
# Update package lists
sudo apt update && sudo apt upgrade -y

# Install Docker using the official convenience script
curl -fsSL https://get.docker.com | sudo sh

# Allow your user to run Docker without sudo
sudo usermod -aG docker $USER

# Log out and back in for the group change to take effect
exit
```

After logging back in, verify Docker is working:

```bash
docker --version
docker compose version
```

Both commands should print version numbers without errors.

---

## 3. Clone the repository

```bash
cd /opt
sudo git clone https://github.com/YOUR_ORG/brygge.git
sudo chown -R $USER:$USER brygge
cd brygge
```

You can place the repository anywhere on the server. `/opt/brygge` is a common choice.

---

## 4. Configure environment

Copy the example configuration file:

```bash
cp deploy/.env.example deploy/.env
```

Open `deploy/.env` in a text editor (e.g. `nano deploy/.env`) and fill in each section. The file contains inline comments explaining every setting.

### Domain

```
DOMAIN=mitt-klubb.no
LETSENCRYPT_EMAIL=styre@mitt-klubb.no
```

- `DOMAIN` -- your club's domain name, without `https://`.
- `LETSENCRYPT_EMAIL` -- an email address for Let's Encrypt certificate notifications. It will only be used if a certificate is about to expire.

### PostgreSQL

```
POSTGRES_DB=brygge
POSTGRES_USER=brygge
POSTGRES_PASSWORD=<a long random password>
DATABASE_URL=postgres://brygge:<password>@db:5432/brygge?sslmode=disable
```

Generate a strong password:

```bash
openssl rand -base64 32
```

Use the same password in both `POSTGRES_PASSWORD` and the `DATABASE_URL` connection string. The hostname `db` refers to the database container and should not be changed.

### Redis

```
REDIS_URL=redis://redis:6379
```

No password is required in the default configuration because Redis is only accessible within the Docker network. The hostname `redis` refers to the container.

### JWT secret

```
JWT_SECRET=<a long random string>
```

Generate one:

```bash
openssl rand -base64 48
```

This secret is used to sign authentication tokens. Keep it private and do not change it after users have logged in, or all sessions will be invalidated.

### Vipps credentials

```
VIPPS_CLIENT_ID=<from Vipps developer portal>
VIPPS_CLIENT_SECRET=<from Vipps developer portal>
VIPPS_SUBSCRIPTION_KEY=<from Vipps developer portal>
VIPPS_MERCHANT_SERIAL=<from Vipps developer portal>
VIPPS_CALLBACK_URL=https://mitt-klubb.no/api/v1/auth/vipps/callback
VIPPS_RETURN_URL=https://mitt-klubb.no/payment/complete
```

See section 7 below for instructions on obtaining Vipps credentials.

### S3-compatible object storage

```
S3_ENDPOINT=https://fsn1.your-objectstorage.com
S3_BUCKET=brygge-documents
S3_ACCESS_KEY=<your access key>
S3_SECRET_KEY=<your secret key>
S3_REGION=fsn1
```

If you are on Hetzner, create an Object Storage bucket in the Hetzner Cloud console. Use the generated credentials here. Documents, harbour charts, and other uploads are stored in this bucket.

### Email (Resend)

```
RESEND_API_KEY=<from resend.com>
EMAIL_FROM=noreply@mitt-klubb.no
```

Sign up at [resend.com](https://resend.com), verify your domain, and copy your API key. The free tier is sufficient for most clubs.

### Club configuration

```
CLUB_SLUG=mitt-klubb
CLUB_NAME=Mitt Klubb
```

The slug is a URL-safe identifier for your club. Use lowercase letters and hyphens only.

---

## 5. Run setup

Make the CLI script executable and run the initial setup:

```bash
chmod +x scripts/brygge.sh
./scripts/brygge.sh setup
```

This will:

1. Build the API image from source.
2. Start the database and Redis.
3. Run database migrations to create the schema.
4. Start all services (API, Traefik, Dendrite, Element, Uptime Kuma).
5. Traefik will automatically provision TLS certificates from Let's Encrypt.

The first run may take a few minutes while images download.

---

## 6. Verify

Check that all services are running:

```bash
./scripts/brygge.sh status
```

All containers should show a status of "Up" or "healthy".

Visit your domain in a browser:

- `https://mitt-klubb.no` -- the main site
- `https://status.mitt-klubb.no` -- the Uptime Kuma status page
- `https://element.mitt-klubb.no` -- Element Web (Matrix client)

If you see a certificate warning, wait a minute and refresh. Traefik needs a moment to complete the Let's Encrypt challenge.

---

## 7. Configure Vipps Login

Brygge uses Vipps Login for authentication and Vipps payments for dues and bookings.

1. Go to the [Vipps developer portal](https://developer.vippsmobilepay.com/).
2. Create a new application (or use an existing one) under your organisation.
3. Under **Login**, set the redirect URI to: `https://mitt-klubb.no/api/v1/auth/vipps/callback`
4. Note the **Client ID**, **Client Secret**, **Subscription Key**, and **Merchant Serial Number**.
5. Enter these values in your `deploy/.env` file.

For testing, Vipps provides a test environment (MT). Switch to production credentials when you are ready to go live.

---

## 8. Set up backups

The `brygge backup` command creates a compressed database dump. Set up a daily cron job to run it automatically.

```bash
# Open the crontab editor
crontab -e
```

Add this line to run a backup every night at 02:00:

```
0 2 * * * /opt/brygge/scripts/brygge.sh backup >> /var/log/brygge-backup.log 2>&1
```

Adjust the path if you cloned the repository to a different location.

Backups are stored in the `backups/` directory inside the project folder. Old backups (more than 30 days) are automatically removed. For extra safety, copy backups to a separate location or object storage bucket periodically.

### Restoring from backup

If you ever need to restore:

```bash
gunzip -c backups/brygge_20260306_020000.sql.gz | \
  docker compose -f deploy/docker-compose.yml exec -T db \
  psql -U brygge brygge
```

---

## 9. Updating

When a new version of Brygge is released:

```bash
cd /opt/brygge
git pull
./scripts/brygge.sh update
```

This rebuilds the API image from source, runs any new database migrations, and restarts services. Downtime is typically a few seconds.

---

## 10. Troubleshooting

### Services fail to start

Check the logs for the specific service:

```bash
./scripts/brygge.sh logs api
./scripts/brygge.sh logs db
./scripts/brygge.sh logs traefik
```

### TLS certificate not provisioning

- Verify your DNS A records are pointing to the correct IP address: `dig mitt-klubb.no`
- Ensure ports 80 and 443 are open in your server's firewall.
- Check Traefik logs for ACME errors: `./scripts/brygge.sh logs traefik`

### Database connection errors

- Confirm the `POSTGRES_PASSWORD` in your `.env` matches the password in `DATABASE_URL`.
- Check that the database container is healthy: `./scripts/brygge.sh status`

### "Permission denied" when running brygge.sh

```bash
chmod +x scripts/brygge.sh
```

### Port 80 or 443 already in use

If another web server (Apache, Nginx) is running, stop it first:

```bash
sudo systemctl stop apache2
sudo systemctl disable apache2
```

### Out of disk space

Check disk usage:

```bash
df -h
docker system df
```

Remove unused Docker resources:

```bash
docker system prune -a
```

### Resetting everything

If you need to start over completely:

```bash
./scripts/brygge.sh stop
docker volume rm $(docker volume ls -q --filter name=brygge)
./scripts/brygge.sh setup
```

This will delete all data including the database. Make a backup first if you have data you want to keep.
