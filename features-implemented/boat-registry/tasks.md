# Boat Registry — Implementation Tasks

## Phase 1: Schema & Data Foundation

### 1.1 Database Migration
- [ ] Create migration `000007_boat_registry.up.sql`:
  - `boat_models` table with manufacturer, model, dimensions, source, external_id, checksum
  - Full-text search index on manufacturer+model
  - Alter `boats`: add boat_model_id, manufacturer, model, weight_kg, measurements_confirmed, confirmed_by, confirmed_at
  - Alter `waiting_list_entries`: add boat_id
  - Alter `slip_assignments`: add boat_id
- [ ] Down migration to revert

### 1.2 Seed Boat Models
- [ ] Curate ~80-100 common Norwegian boat models with dimensions:
  - Motorboats: Askeladden, Yamarin, Ibiza, Marex, Nimbus, Nordkapp, Buster, Quicksilver
  - Sailboats: Jeanneau, Bavaria, Beneteau, Hallberg-Rassy, Dehler, Hanse, Dufour, Nauticat
  - Small boats: Pioner, Ryds, Terhi, Rana
- [ ] Import ORC dataset for additional sailboat coverage (parse `orc-data.json`)
- [ ] Add to seed script or create separate import command

### 1.3 Boat Model Search Endpoint
- [ ] `GET /api/v1/boat-models?q=` — full-text search, returns top 20 matches
- [ ] Handler in `backend/internal/handlers/boat_models.go`
- [ ] No auth required (public endpoint for the search)

## Phase 2: Enhanced Boat CRUD with Confirmation

### 2.1 Backend: Boat Create/Update Logic
- [ ] Update `HandleCreateBoat`:
  - Accept `boat_model_id`, `manufacturer`, `model`, `weight_kg`
  - If `boat_model_id` set and dimensions match model → `measurements_confirmed = true`
  - If custom dimensions → `measurements_confirmed = false`
- [ ] Update `HandleUpdateBoat`:
  - If dimensions changed on a confirmed boat → reset confirmed fields
  - If dimensions now match a linked model → re-auto-confirm
  - Return updated confirmation status
- [ ] Update boat list/get responses to include confirmation fields

### 2.2 Frontend: Enhanced Boats View
- [ ] Add type-ahead search bar for boat models (debounced, calls `/api/v1/boat-models?q=`)
- [ ] Selecting a model pre-fills manufacturer, model, dimensions
- [ ] Show confirmation badge: green "Godkjent" / yellow "Venter på godkjenning"
- [ ] Confirmation warning dialog when editing dimensions on a confirmed boat
- [ ] Show slip assignment indicator if boat is on a slip
- [ ] Show waiting list indicator if boat is linked to a WL entry

### 2.3 Admin: Boat Confirmation Queue
- [ ] `GET /api/v1/admin/boats/unconfirmed` — list boats needing confirmation
- [ ] `POST /api/v1/admin/boats/{boatID}/confirm` — confirm measurements, optionally adjust
  - Sets confirmed_by, confirmed_at, measurements_confirmed
  - Option to add model to boat_models if it doesn't exist
- [ ] Frontend: new admin view `/admin/boats` with confirmation queue
- [ ] Add to admin sidebar (under Havn group)

## Phase 3: Beam-Based Slip Pricing

### 3.1 Backend: Pricing Tier Resolution
- [ ] Add `resolveSlipFee(beamM, clubID)` function
  - Queries `price_items` where `category = 'slip_fee'` and beam in metadata range
  - Returns matching tier or nil
- [ ] Add beam range validation for slip_fee price items (no overlaps)

### 3.2 Admin: Pricing UI Enhancement
- [ ] When category is `slip_fee`, show beam_min/beam_max inputs
- [ ] Visual tier list showing beam ranges
- [ ] Validation warnings for overlapping or gap ranges
- [ ] Existing pricing CRUD still works for other categories

### 3.3 Integrate with Invoice Generation
- [ ] When generating annual slip fees, look up boat beam → resolve tier → set amount
- [ ] If boat unconfirmed or no boat linked → flag for manual review (no auto-invoice)
- [ ] Show resolved fee on slip detail pages (admin + portal)

## Phase 4: Waiting List & Slip Assignment Integration

### 4.1 Waiting List + Boat Link
- [ ] Update `HandleJoinWaitingList` to accept optional `boat_id`
- [ ] Add `PUT /api/v1/waiting-list/me/boat` to update linked boat
- [ ] Enforce: styre cannot offer a slip if entry has no boat linked
- [ ] Update portal waiting list to show boat beam column
- [ ] Update admin waiting list to show full boat details + confirmation status

### 4.2 Slip Assignment + Boat
- [ ] Update `HandleAssignSlip` to accept `boat_id`
- [ ] When user accepts offer, link their waiting list boat to the slip assignment
- [ ] Enforce: one boat per active slip assignment
- [ ] Show boat info on slip detail pages

### 4.3 Seed Data Update
- [ ] Give test users boats (Kari has a boat on her slip, Per has a boat on the waiting list)
- [ ] Seed some unconfirmed boats for the confirmation queue

## Phase 5: Future — Shared Dataset

(Not implemented now, schema supports it)

- [ ] Extract confirmed boat models to `brygge-klubb/boatdata` repo
- [ ] Periodic sync: push `club-confirmed` models, pull latest dataset
- [ ] Dedup via `external_id` and `checksum` fields
- [ ] Admin UI to trigger sync manually
