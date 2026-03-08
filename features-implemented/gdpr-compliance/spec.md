# GDPR / Personopplysningsloven Compliance

## Overview

Ensure compliance with Norwegian Personopplysningsloven (implementing EU GDPR).
Covers legal templates, technical data handling, and strictly necessary cookies only.

## Legal Templates

Generate customizable Norwegian-language templates that clubs can edit:

### Personvernerklaering (Privacy Policy)
- What data is collected (name, email, phone, address, boat details, payment info)
- Legal basis for processing (contract fulfillment for membership, legitimate interest for club operations)
- Data retention periods
- Third-party processors (Vipps, hosting provider)
- Rights of data subjects
- Contact information for data controller (club board)

### Databehandleravtale (Data Processing Agreement)
- Between the club (data controller) and the platform operator (data processor)
- Standard Datatilsynet-recommended clauses
- Sub-processor list
- Security measures description
- Breach notification procedures

## Technical Features

### Right of Access (Innsynsrett)
- Member can request full data export from their profile
- Export format: JSON + PDF summary
- Includes: profile data, boat registrations, waiting list history, booking history,
  payment history, forum posts, document comments, audit log entries
- One-click "Last ned mine data" button in profile

### Right to Erasure (Rett til sletting)
- Member can request account deletion from profile
- Process:
  1. Member clicks "Slett min konto"
  2. Confirmation dialog explaining consequences (loss of slip, waiting list position, etc.)
  3. 14-day grace period (can cancel during this time)
  4. After grace period: anonymize personal data, retain financial records (required by
     Bokforingsloven — 5 year retention for accounting records)
- Anonymization approach: replace name with "Slettet bruker", null email/phone/address,
  retain anonymized transaction records
- Admin can also process deletion requests manually

### Admin Data Management
- Admin view: list of pending deletion requests with grace period countdown
- Admin can process data export requests on behalf of members
- Data retention dashboard: overview of what data is stored and for how long

### Consent Tracking
- Record consent timestamps for: terms of service, privacy policy
- Re-consent required when privacy policy is updated (show banner on next login)
- Consent log stored in `user_consents` table

## Cookie Policy

**Strictly necessary cookies only** — no consent banner required:
- Session/auth JWT token (httpOnly cookie or localStorage)
- CSRF token (if applicable)
- Language preference

No analytics cookies, no tracking pixels, no third-party cookies.
If analytics are added later, a cookie consent mechanism must be implemented.

## Schema

```sql
CREATE TABLE user_consents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    consent_type TEXT NOT NULL,           -- 'terms', 'privacy_policy'
    version     TEXT NOT NULL,             -- policy version string
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_user_consents_user ON user_consents(user_id);

CREATE TABLE deletion_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    grace_end       TIMESTAMPTZ NOT NULL,  -- requested_at + 14 days
    cancelled_at    TIMESTAMPTZ,
    processed_at    TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'pending'  -- pending, cancelled, processed
);

-- Privacy policy / terms documents
CREATE TABLE legal_documents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id),
    doc_type    TEXT NOT NULL,              -- 'privacy_policy', 'terms', 'dpa'
    version     TEXT NOT NULL,
    content     TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## API Endpoints

- `GET /api/v1/members/me/data-export` — generate and download personal data
- `POST /api/v1/members/me/delete-request` — initiate account deletion
- `DELETE /api/v1/members/me/delete-request` — cancel pending deletion
- `GET /api/v1/admin/deletion-requests` — list pending requests
- `POST /api/v1/admin/deletion-requests/{id}/process` — execute deletion
- `GET /api/v1/legal/{docType}` — get current published legal document
- `POST /api/v1/members/me/consent` — record consent for a document version

## Bokforingsloven Retention

Norwegian accounting law requires 5-year retention of financial records.
When anonymizing a deleted user, payment/invoice records are retained with
anonymized references (user_id nulled, name replaced with "Slettet bruker").

## Implementation Notes

- Privacy policy and DPA templates should be seeded with Norwegian boilerplate
  that clubs customize via admin editor
- Data export should be rate-limited (max 1 per 24h) to prevent abuse
- Deletion grace period job: daily cron/background worker checks for expired grace periods
