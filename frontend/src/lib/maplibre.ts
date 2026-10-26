import * as maplibregl from 'maplibre-gl'
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url'

// maplibre-gl 6 starts its worker from a file next to its own module. Vite
// moves that module (pre-bundling in dev, hashed chunks in a build) without
// the worker beside it, so the worker is bundled by Vite and its URL set
// explicitly. Import maplibre through here so this runs before any map.
maplibregl.setWorkerUrl(workerUrl)

export { maplibregl }
