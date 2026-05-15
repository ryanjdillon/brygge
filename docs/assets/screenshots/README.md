# README screenshots

The root `README.md` embeds these. All captured with Playwright against the
local seeded stack at 1440×900, demo-auth enabled.

| File | Page | Auth |
|------|------|------|
| `landing.png` | Public club landing (`/`) | none |
| `harbor-map.png` | Public guest-harbour + chart (`/harbor`) | none |
| `dashboard.png` | Admin overview / event management (`/admin`) | demo-login + real TOTP verify |
| `slips.png` | Slip register (`/admin/slips`) | demo-login + real TOTP verify |

## How admin pages are captured (no bypass)

Admin routes are behind the `RequireAdminTOTP` step-up gate. `cmd/seed`
enrolls `admin@brygge.local` in TOTP exactly as a real user would (encrypted
secret via `TOTP_ENCRYPTION_KEY`, `totp_enabled = true`); the plaintext base32
secret is written to the gitignored `backend/.seed-totp-secret`. The capture
flow demo-logs-in, computes the current code (`just totp`), and submits the
**real** `/admin/verify-totp` form. No middleware bypass exists.

`just totp` prints the current 6-digit code for manual local use.

## Regenerating manually

Bring the stack up and seed (`just setup` / `just up`), then:

```
# public, no auth:
#   http://localhost:5173/        → landing.png
#   http://localhost:5173/harbor  → harbor-map.png
#
# admin (demo-login admin@brygge.local, then /admin/verify-totp with `just totp`):
#   http://localhost:5173/admin        → dashboard.png
#   http://localhost:5173/admin/slips  → slips.png
```

Capture at 1440×900, keep each under ~500 KB. Full automation (the GitHub
Actions workflow) is tracked in Linear as DIL-331.
