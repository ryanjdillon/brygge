# TODO

## Code Quality

- [ ] Rename all Norwegian/partial-Norwegian identifiers to English (e.g. `Sakliste`, `SaklisteItem`, `dugnad`)

## API

- [ ] Register remaining 2 unregistered endpoints in OpenAPI spec and migrate to typed client
  - `DocumentsView.vue`: document comments (GET/POST `/api/v1/documents/{docID}/comments`) — backend handlers don't exist yet
  - `WaitingListView.vue`: decline offer (POST `/api/v1/waiting-list/{entryID}/decline`) — backend handler doesn't exist yet

## Infrastructure

- [ ] Automatic scheduled backups to cloud object storage
  - Cron-driven pg_dump compressed and uploaded to S3-compatible storage (Hetzner Object Storage, Backblaze B2, etc.)
  - Configurable retention policy (e.g. daily for 7 days, weekly for 4 weeks, monthly for 12 months)
  - Environment variables: `BACKUP_S3_ENDPOINT`, `BACKUP_S3_BUCKET`, `BACKUP_S3_ACCESS_KEY`, `BACKUP_S3_SECRET_KEY`
  - Health check endpoint or notification on backup failure (email or webhook)
  - Could run as a sidecar container or scheduled Docker job alongside the existing `brygge.sh backup`

## Features

- [ ] Add configurable default language per club
  - Use for AI summarization/extraction language config (replace hardcoded Norwegian prompts in `ai/` package)
  - Default language for public landing pages
  - Could inform i18n locale selection for members

## Multi-Tenant Generalization

- [ ] Generalize the platform to support different organization types beyond harbour clubs
  - Target use cases: sports clubs, private cabin owner associations, cabin clubs (e.g. BSI Friluft, DNT)
  - Rename `club_id` → `org_id` (and related naming throughout DB, API, middleware claims)
  - Make domain-specific modules toggleable per org (slips, waiting list, boats → harbour; hoist, bookings → generic)
  - Expand existing feature flags (`cfg.Features.*`) into per-org module configuration
  - Abstract harbour-specific terminology in UI (slips → units/spaces, boats → assets, etc.)
