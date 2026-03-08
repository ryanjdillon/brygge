# Boat Registry & Measurement Confirmation System

## Overview

Enhanced boat management with a shared boat model database, measurement confirmation workflow,
beam-based slip pricing tiers, and integration with the waiting list and slip assignment systems.

## Goals

1. Users can search for their boat model and auto-populate dimensions
2. Custom/unverified measurements require styre/havnesjef confirmation
3. Confirmed measurements cannot be silently changed — edits reset confirmation
4. Slip fees are calculated from the boat's beam (bredde)
5. Waiting list entries indicate which boat the user is applying with
6. Only one boat per user can occupy a slip
7. The boat model dataset is designed for future sharing across clubs

## Data Sources

### Available Open Data

- **ORC Certificate Data** — Free, open source (`jieter/orc-data` on GitHub). ~thousands of racing
  sailboats with LOA, beam, draft, displacement. JSON format. Good for sailboat coverage.
- **Småbåtregisteret** — Norwegian registry via Infotorg. Requires formal authorization + fees.
  Not viable for MVP but could be integrated later.
- **No free general boat model API exists** — TheBoatDB, SailboatData.com, etc. are all
  web-only or paid. No usable free API for motorboats.

### Strategy

Local `boat_models` table as the single source of truth:

1. **Seed data** — Curated list of ~80-100 common Norwegian boat models (Askeladden, Yamarin,
   Ibiza, Jeanneau, Bavaria, Beneteau, Hallberg-Rassy, Dehler, Marex, Nimbus, etc.)
2. **ORC import** — One-time import for sailboat coverage
3. **Club-confirmed additions** — When styre confirms a custom boat and its model doesn't exist,
   the model is added to `boat_models` with `source = 'club-confirmed'`
4. **Future sync** — The dataset moves to `brygge-klubb/boatdata` as a shared repo. Individual
   Brygge installations periodically contribute confirmed models and pull the latest dataset.

### Future: Shared Dataset (`brygge-klubb/boatdata`)

- Canonical JSON/CSV in a separate Git repo
- Each Brygge instance has a `source` + `external_id` on `boat_models` for dedup
- Sync mechanism: clubs push `club-confirmed` models upstream, pull latest periodically
- Dataset grows organically as more clubs use the system
- Not implemented now — schema is designed to support it later

## Schema Changes

### New table: `boat_models`

```sql
CREATE TABLE boat_models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    manufacturer    TEXT NOT NULL,
    model           TEXT NOT NULL,
    year_from       INT,
    year_to         INT,
    length_m        NUMERIC,
    beam_m          NUMERIC,
    draft_m         NUMERIC,
    weight_kg       NUMERIC,
    boat_type       TEXT NOT NULL DEFAULT '',    -- sailboat, motorboat, rowboat, etc.
    source          TEXT NOT NULL DEFAULT 'seed', -- seed, orc-import, club-confirmed
    external_id     TEXT NOT NULL DEFAULT '',     -- for dedup when syncing
    checksum        TEXT NOT NULL DEFAULT '',     -- hash of dimensions for change detection
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_boat_models_manufacturer ON boat_models(manufacturer);
CREATE INDEX idx_boat_models_search ON boat_models
    USING gin (to_tsvector('simple', manufacturer || ' ' || model));
```

### Alter: `boats`

```sql
ALTER TABLE boats
    ADD COLUMN boat_model_id          UUID REFERENCES boat_models(id),
    ADD COLUMN manufacturer           TEXT NOT NULL DEFAULT '',
    ADD COLUMN model                  TEXT NOT NULL DEFAULT '',
    ADD COLUMN weight_kg              NUMERIC,
    ADD COLUMN measurements_confirmed BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN confirmed_by           UUID REFERENCES users(id),
    ADD COLUMN confirmed_at           TIMESTAMPTZ;
```

### Alter: `waiting_list_entries`

```sql
ALTER TABLE waiting_list_entries
    ADD COLUMN boat_id UUID REFERENCES boats(id);
```

### Alter: `slip_assignments`

```sql
ALTER TABLE slip_assignments
    ADD COLUMN boat_id UUID REFERENCES boats(id);
```

## Confirmation Workflow

### Auto-confirmed (trusted source)

1. User searches `boat_models`, picks a match
2. Dimensions auto-filled from the model
3. `measurements_confirmed = true`, `boat_model_id` set
4. User can still override name, registration number, year

### Manual confirmation (custom entry)

1. User enters dimensions manually (no model match, or overrides model dimensions)
2. `measurements_confirmed = false`, `boat_model_id` may or may not be set
3. Entry appears in styre/havnesjef confirmation queue
4. Styre reviews, can adjust dimensions, then confirms
5. Sets `confirmed_by`, `confirmed_at`, `measurements_confirmed = true`
6. If the boat's manufacturer+model combo doesn't exist in `boat_models`,
   option to add it (with `source = 'club-confirmed'`)

### Edit after confirmation

1. User can always edit their boat (name, reg number, AND dimensions)
2. If editing dimensions on a confirmed boat → **confirmation warning dialog**:
   "Endring av mål vil kreve ny godkjenning fra styret. Vil du fortsette?"
3. If user confirms → `measurements_confirmed = false`, `confirmed_by = NULL`,
   `confirmed_at = NULL`
4. Boat re-enters styre confirmation queue

### Backend enforcement

- The backend does NOT block dimension edits — it accepts them but resets confirmation
- Confirmation status is recalculated server-side (not trusted from client)
- If `boat_model_id` is set AND dimensions match the model → auto-confirm
- If dimensions differ from model → unconfirmed regardless of `boat_model_id`

## Beam-Based Slip Pricing

### Using `price_items` with metadata

Slip fee tiers stored as `price_items` with `category = 'slip_fee'` and beam range in metadata:

```json
{"beam_min": 0, "beam_max": 2.5}      → name: "Plassleie ≤ 2.5m bredde", amount: 6000
{"beam_min": 2.5, "beam_max": 3.5}    → name: "Plassleie 2.5–3.5m bredde", amount: 8500
{"beam_min": 3.5, "beam_max": 4.5}    → name: "Plassleie 3.5–4.5m bredde", amount: 12000
{"beam_min": 4.5, "beam_max": 99}     → name: "Plassleie > 4.5m bredde", amount: 15000
```

### Tier resolution

```
func resolveSlipFee(beamM float64, tiers []PriceItem) *PriceItem
```

- Match `beam_min <= beam_m < beam_max`
- If no match or boat unconfirmed → return nil (no auto-invoicing)
- Used when generating annual slip fee invoices

### Admin management

- Existing pricing admin (`/admin/pricing`) already supports CRUD on `price_items`
- Add UI hint for `slip_fee` category to show/edit beam range fields from metadata
- Admin can create any number of tiers with any beam ranges
- Validation: ranges must not overlap, must cover all beams (warning if gaps)

## Waiting List Integration

- `waiting_list_entries.boat_id` — which boat the user is applying with
- Not strictly required to join (user can join, then add boat later)
- Required before styre can make an offer (backend enforces)
- Portal waiting list shows boat info: name, length, beam (privacy-masked for others,
  but dimensions visible so users understand slip sizing)
- Admin waiting list shows full boat details per entry

## Slip Assignment Integration

- `slip_assignments.boat_id` — which boat occupies the slip
- Set when styre assigns a slip (or when user accepts an offer)
- Enforced: a user can own multiple boats, but only one boat per active slip assignment
- Changing the boat on a slip requires releasing and re-assigning (or an admin override)

## UI Changes

### Portal: Boats View (enhanced)

- **Search bar** at top: type-ahead search against `boat_models`
  - Shows: "Manufacturer Model (Year) — L×B×D"
  - Selecting a model pre-fills the form
- **Boat form**: manufacturer, model, year, name, reg number, length, beam, draft, weight
- **Confirmation badge**: green "Godkjent" or yellow "Venter på godkjenning"
- **Edit warning**: when editing dimensions on confirmed boat, show confirmation dialog
- **Slip indicator**: if boat is assigned to a slip, show which slip
- **Waiting list indicator**: if boat is linked to a waiting list entry, show position

### Portal: Waiting List View (enhanced)

- Show boat name + dimensions next to user's entry
- In the full list table: add beam column (visible for all, useful for understanding sizing)

### Admin: Boat Confirmation Queue (new)

- List of boats with `measurements_confirmed = false`
- Show: owner name, boat details, dimensions, whether a model match exists
- Actions: Confirm (sets confirmed), Adjust + Confirm, Add to boat models
- Filter by pending only

### Admin: Pricing (enhanced)

- When category is `slip_fee`, show beam range inputs (beam_min, beam_max)
- Visual tier list showing the beam ranges and amounts
- Validation warnings for overlapping or gap ranges

### Admin: Waiting List (enhanced)

- Show boat name, beam, confirmation status per entry
- Highlight entries without a boat linked (can't offer until boat is set)

## API Endpoints

### Boat Models

```
GET  /api/v1/boat-models?q={search}&type={type}  — search boat models (public)
```

### Boats (existing, enhanced)

```
GET    /api/v1/members/me/boats                — list my boats (add confirmation fields)
POST   /api/v1/members/me/boats                — create boat (auto-confirm if model match)
PUT    /api/v1/members/me/boats/{boatID}       — update boat (reset confirm if dims changed)
DELETE /api/v1/members/me/boats/{boatID}        — delete boat
```

### Boat Confirmation (admin)

```
GET    /api/v1/admin/boats/unconfirmed          — list unconfirmed boats
POST   /api/v1/admin/boats/{boatID}/confirm     — confirm measurements
```

### Waiting List (enhanced)

```
POST   /api/v1/waiting-list/join               — add boat_id param
PUT    /api/v1/waiting-list/me/boat             — update linked boat
```
