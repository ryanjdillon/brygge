# Two-Factor Authentication (TOTP)

Brygge protects the styre admin area and high-blast-radius operations with TOTP-based 2FA on top of magic-link sessions. A leaked session cookie alone is not enough to grant admin access or perform irreversible actions like role changes or member deletion.

This page covers enrollment (for board members), recovery (for the user themselves), and admin reset (for the case where both factors are lost).

## How the gate works

Two layers, both implemented in `backend/internal/middleware/session.go`:

| Layer | Window | When it triggers | What the user sees |
|-------|--------|------------------|--------------------|
| Session-level (`RequireAdminTOTP`) | 12 hours | Every `/admin/*` route | Full-page redirect to `/admin/verify-totp` |
| Per-action (`RequireFreshTOTP`) | 5 minutes | Role grants/revokes, member deletion, recovery-code rotation, admin TOTP reset | In-context modal that replays the failed request after success |

A user with admin-tier role (`admin`, `board`, `treasurer`, or `harbor_master`) who hasn't enrolled TOTP simply cannot reach the admin area — the nav link is replaced with an "Enable 2FA" prompt that links to the enrollment page.

## Enrollment (board members)

Anyone with an admin-tier role enrolls themselves at `/portal/security`. No SQL, no admin reset, no shell access.

1. **Sign in** at `https://<your-club-domain>/login` and click the magic link.
2. Click **"Enable 2FA"** in the nav (the amber prompt). Or navigate directly to **My page → Security**.
3. Click **"Enable 2FA"**. A QR code appears alongside a manual base32 key.
4. Open your authenticator app (Google Authenticator, Authy, 1Password, Bitwarden — anything that supports TOTP). Scan the QR code, or paste the manual key.
5. Type the **6-digit code** from the authenticator into the form and click **"Confirm"**.
6. **Save your 10 recovery codes.** They appear once and never again. Use **Copy all** or **Download** and store them in your password manager. Tick **"I've saved the codes"** before continuing.

That's it. The next time you click "Admin", you'll be asked for a TOTP code (12-hour step-up window). For sensitive actions like changing roles or deleting members, you'll be asked again (5-minute window).

### What if I want to re-enroll?

Re-running enrollment overwrites the existing secret and clears any previously-issued recovery codes. Useful if you suspect your authenticator app was compromised, or if you're switching apps. Disabling first isn't necessary.

## Recovery (lost authenticator)

If you've lost access to your authenticator app but still have your recovery codes:

1. Sign in via magic link as usual.
2. When prompted for a TOTP code (`/admin/verify-totp` page or the in-context modal), click **"Use a recovery code instead"**.
3. Type one of your saved codes (e.g. `ABCD-EFGH`). Codes are case-insensitive and the dash is optional.
4. The code is consumed (single-use). The session is unlocked for 12 hours.
5. Once you're back into the admin area, go to **My page → Security → Generate new recovery codes** to issue a fresh batch. The remaining unused codes are wiped at the same time.

The response from the recover endpoint includes `codes_remaining` so the SPA can warn you when you're running low. Generating new codes requires a fresh TOTP code (so an attacker with a stale session cookie can't lock you out by rotating the codes).

## Admin reset (lost authenticator AND recovery codes)

A board member is locked out completely — phone wiped, password manager lost. Another admin can disable the user's TOTP so they can re-enroll.

**Prerequisites:**
- The acting admin has the `admin` role (not just `board`).
- The acting admin is themselves fresh-TOTP-verified within the last 5 minutes.

**Procedure** (currently via API, until the admin UI lands):

```bash
# 1. Find the locked-out user's ID
# In the admin UI: Users → search → copy the UUID

# 2. As the acting admin, browser sign-in + step-up so your session has fresh TOTP

# 3. POST to the reset endpoint
curl -X POST 'https://<your-club-domain>/api/v1/admin/users/<target-user-id>/totp/disable' \
  --cookie 'brygge_session=<your-session-cookie>'
```

The endpoint runs in a single transaction:
- Wipes the target user's `totp_secret_encrypted` and sets `totp_enabled = false`
- Deletes all their unused recovery codes
- Revokes all their active sessions (forces re-login on next request)

Audit log entry is written: `actor=<admin>, action=admin.totp_disabled_by_admin, target=<user>`.

The locked-out user signs in via magic link as normal, then re-enrolls at `/portal/security`.

## What if all admins lose their devices?

A genuine all-keys-down scenario — every admin lost both authenticator and recovery codes simultaneously. Manual DB intervention is the only recourse:

```bash
# On the production VM, as a user with postgres access:
sudo -u postgres psql -d brygge -c "
  UPDATE users SET totp_secret_encrypted = NULL, totp_enabled = false
  WHERE email = '<your-email>';
  DELETE FROM totp_recovery_codes WHERE user_id IN (
    SELECT id FROM users WHERE email = '<your-email>'
  );
  DELETE FROM sessions WHERE user_id IN (
    SELECT id FROM users WHERE email = '<your-email>'
  );
"
```

Then sign in via magic link and re-enroll. After that, use the admin reset endpoint above for any other locked-out admins.

This intervention should be rare — recovery codes solve the common case, and admins should seed their codes in shared password vaults so a single device loss doesn't escalate.

## Server-side state

| Where | What |
|-------|------|
| `users.totp_secret_encrypted` (BYTEA) | AES-256-GCM-encrypted TOTP secret |
| `users.totp_enabled` (BOOLEAN) | Quick check for "is the gate active" |
| `sessions.totp_verified_at` (TIMESTAMPTZ) | When this session last passed the gate; nil = never |
| `totp_recovery_codes(user_id, code_hash, used_at)` | bcrypt-hashed single-use codes |

The encryption key for the TOTP secret comes from `TOTP_ENCRYPTION_KEY` in `/etc/brygge/env` (32 bytes, hex-encoded). Rotating that key is non-trivial — every existing TOTP enrollment becomes unrecoverable, requiring all users to re-enroll. Don't rotate without a planned outage.

## Related

- [setup.md](../developer/setup.md) — first-time deploy
- [configuration.md](../developer/configuration.md) — env var reference (search `TOTP_ENCRYPTION_KEY`)
- [troubleshooting.md](../developer/troubleshooting.md) — TOTP-specific entries
