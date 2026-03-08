# Sjokart — Sea Chart Integration

## Overview

Embed interactive sea charts on the directions page using free Kartverket WMS/WMTS
tile services. Two-zoom design: harbour entrance detail and regional overview.
Support coordinate export for marine plotters.

## Map Views

### 1. Harbour Entrance (Default / Zoomed In)
- Centered on club harbour entrance coordinates
- Zoom level ~15-16 (shows individual piers, breakwaters)
- Markers for:
  - Harbour entrance approach point
  - Guest slip area
  - Fuel station (if applicable)
  - Club building
- Depth annotations from Kartverket chart data (rendered on tiles)

### 2. Regional Overview (Zoomed Out)
- Zoom level ~11-12 (shows surrounding coastline, nearby harbours)
- Club location marker with label
- Toggle between views via zoom buttons or a "Vis region" / "Vis havn" toggle

## Technical Implementation

### Map Library
- **MapLibre GL JS** (open-source, WebGL-accelerated, works well in PWA on iOS + Android)
- Fallback: Leaflet with raster tiles if MapLibre causes issues on older devices

### Tile Source
- **Kartverket Sjokart** WMTS:
  `https://cache.kartverket.no/v1/wmts/1.0.0/sjokart/default/webmercator/{z}/{y}/{x}.png`
- Free for public use, no API key required
- Sea chart tiles include depth soundings, navigational aids, buoys

### Club Configuration
- Admin sets harbour coordinates (lat/lng) — already exists in club config
- Admin can add/edit map markers via simple form:
  - Type: `entrance`, `guest_slip`, `fuel`, `building`, `waypoint`
  - Coordinates (lat/lng)
  - Label text
- Approach waypoints: ordered list of coordinates defining the approach route
  (rendered as a dashed line on the map)

### Coordinate Export
- "Eksporter koordinater" button on the map page
- Export formats:
  - **GPX** (standard, works with most plotters and apps: Navionics, OpenCPN, etc.)
  - **KML** (Google Earth / Maps)
- Export includes: harbour position, approach waypoints, guest slip locations
- File download with club name in filename (e.g., `brygge-moss-waypoints.gpx`)

## PWA Compatibility

- MapLibre GL works in standalone PWA mode on both iOS and Android
- Tiles are loaded over HTTPS (no CORS issues with Kartverket)
- Consider caching the most-used zoom levels in the service worker for offline
  viewing (harbour entrance view only — ~50 tiles)

## Schema

```sql
CREATE TABLE map_markers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id),
    marker_type TEXT NOT NULL,           -- entrance, guest_slip, fuel, building, waypoint
    label       TEXT NOT NULL DEFAULT '',
    lat         NUMERIC NOT NULL,
    lng         NUMERIC NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0,  -- for waypoint ordering
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## API Endpoints

- `GET /api/v1/map/markers` — public, returns all markers for the club
- `POST /api/v1/admin/map/markers` — create marker
- `PUT /api/v1/admin/map/markers/{id}` — update marker
- `DELETE /api/v1/admin/map/markers/{id}` — delete marker
- `GET /api/v1/map/export/{format}` — download GPX or KML file

## Frontend

- Replace or enhance existing static directions page with interactive map
- Map component: `<SeaChart />` using MapLibre
- Responsive: full-width on mobile, side-by-side with text directions on desktop
- Minimal UI: zoom toggle, marker popups on tap, export button
- Accessible: map has `aria-label`, markers are keyboard-focusable,
  text directions remain available below the map for screen readers

## Dependencies

- `maplibre-gl` npm package (~200KB gzipped)
- No backend dependencies beyond standard HTTP for tile proxying (not needed — direct to Kartverket)
