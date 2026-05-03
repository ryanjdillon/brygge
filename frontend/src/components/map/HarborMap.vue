<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, shallowRef } from 'vue'
import maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'

// Inject __publicField helper into maplibre's GeoJSON workers (vite dev
// bundles the worker separately and esbuild's helper is missing there).
{
  const helperBlob = new Blob(
    [
      'self.__publicField = (obj, key, value) => { Object.defineProperty(obj, key, { enumerable: true, configurable: true, writable: true, value }); return value; };',
    ],
    { type: 'application/javascript' },
  )
  const helperUrl = URL.createObjectURL(helperBlob)
  void maplibregl.importScriptInWorkers(helperUrl)
}
import {
  isFinger,
  isSlip,
  formatSlipLabel,
  type HarborLayoutResponse,
  type SlipFeature,
  type FingerFeature,
} from '@/composables/useHarborLayout'

interface Props {
  layout: HarborLayoutResponse
  highlightSlipId?: string | null
  /** When true, placed slip markers can be dragged. */
  draggableSlips?: boolean
  /** When set, hides this dock's pinned label (so a centered ghost label can take over while editing). */
  hiddenDockSlug?: string | null
  /** Multiplier for slip + dock label sizes (1 = default). */
  labelScale?: number
  /** Default center if the layout has no placed features yet. */
  fallbackCenter?: [number, number]
  fallbackZoom?: number
}

const props = withDefaults(defineProps<Props>(), {
  highlightSlipId: null,
  draggableSlips: false,
  hiddenDockSlug: null,
  labelScale: 1,
  fallbackCenter: () => [5.155736, 60.224303],
  fallbackZoom: 18,
})

const emit = defineEmits<{
  (e: 'select', slip: SlipFeature): void
  (e: 'select-finger', finger: FingerFeature): void
  (e: 'slip-dragend', payload: { id: string; lng: number; lat: number }): void
  (e: 'map-ready', map: maplibregl.Map): void
}>()

const container = ref<HTMLDivElement>()
const map = shallowRef<maplibregl.Map | null>(null)
const slipMarkers = new Map<string, maplibregl.Marker>()

const placedSlips = computed<SlipFeature[]>(() =>
  props.layout.features.filter(
    (f): f is SlipFeature => isSlip(f) && f.geometry != null,
  ),
)
const fingers = computed<FingerFeature[]>(() =>
  props.layout.features.filter(isFinger),
)

const fingersCollection = computed(() => ({
  type: 'FeatureCollection' as const,
  features: fingers.value.map((f) => ({
    ...f,
    properties: { ...f.properties, _id: f.id },
  })),
}))

const docksCollection = computed(() => ({
  type: 'FeatureCollection' as const,
  features: (props.layout.docks ?? [])
    .filter(
      (d) =>
        d.default_lng != null &&
        d.default_lat != null &&
        d.slug !== props.hiddenDockSlug,
    )
    .map((d) => ({
      type: 'Feature' as const,
      geometry: {
        type: 'Point' as const,
        coordinates: [d.default_lng as number, d.default_lat as number],
      },
      properties: {
        _slug: d.slug,
        name: `Dock ${d.name}`,
      },
    })),
}))

function buildSlipMarkerEl(slip: SlipFeature, highlighted: boolean): HTMLElement {
  const root = document.createElement('div')
  root.className = 'slip-marker'
  const occupied = Boolean(
    slip.properties.occupant_id || slip.properties.occupant_last_name,
  )
  const seasonal = slip.properties.assignment_type === 'seasonal'
  const fill = occupied ? (seasonal ? '#f59e0b' : '#0ea5e9') : 'transparent'
  const stroke = highlighted
    ? '#dc2626'
    : occupied
      ? '#0c4a6e'
      : '#94a3b8'
  const text = occupied ? '#ffffff' : '#0f172a'
  const label = formatSlipLabel(slip.properties.section, slip.properties.number)
  const owner =
    slip.properties.occupant_last_name ||
    slip.properties.occupant_name ||
    ''
  const s = props.labelScale

  root.style.cssText =
    'display:flex;flex-direction:column;align-items:center;gap:2px;cursor:pointer;'
  root.innerHTML = `
    <div style="
      min-width:${30 * s}px;height:${20 * s}px;padding:0 ${6 * s}px;
      display:flex;align-items:center;justify-content:center;
      background:${fill};
      border:${Math.max(1, 2 * s)}px solid ${stroke};
      border-radius:${3 * s}px;
      color:${text};
      font:600 ${11 * s}px/1 system-ui,sans-serif;
      box-shadow:0 1px 3px rgba(0,0,0,.25);
      white-space:nowrap;">${label}</div>
    ${owner ? `<div style="
      font:500 ${10 * s}px/1.1 system-ui,sans-serif;
      color:#0f172a;
      background:rgba(255,255,255,.85);
      padding:${1 * s}px ${4 * s}px;border-radius:${2 * s}px;
      white-space:nowrap;
      box-shadow:0 1px 2px rgba(0,0,0,.15);">${escapeHtml(owner)}</div>` : ''}
  `
  return root
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]!),
  )
}

function syncSlipMarkers() {
  const m = map.value
  if (!m) return
  const wanted = new Set<string>()
  for (const slip of placedSlips.value) {
    if (!slip.geometry) continue
    wanted.add(slip.id)
    const highlighted = slip.id === props.highlightSlipId
    const existing = slipMarkers.get(slip.id)
    const el = buildSlipMarkerEl(slip, highlighted)
    el.addEventListener('click', (ev) => {
      ev.stopPropagation()
      emit('select', slip)
    })
    if (existing) {
      existing.getElement().replaceWith(el)
      // maplibre's Marker keeps a reference to its element, so swap-in
      // requires a fresh marker for the new element to bind events.
      existing.remove()
    }
    const marker = new maplibregl.Marker({
      element: el,
      draggable: props.draggableSlips,
      anchor: 'center',
    })
      .setLngLat(slip.geometry.coordinates)
      .addTo(m)
    if (props.draggableSlips) {
      marker.on('dragend', () => {
        const ll = marker.getLngLat()
        emit('slip-dragend', { id: slip.id, lng: ll.lng, lat: ll.lat })
      })
    }
    slipMarkers.set(slip.id, marker)
  }
  for (const [id, marker] of slipMarkers) {
    if (!wanted.has(id)) {
      marker.remove()
      slipMarkers.delete(id)
    }
  }
}

function clearSlipMarkers() {
  for (const marker of slipMarkers.values()) marker.remove()
  slipMarkers.clear()
}

function fitToContent(m: maplibregl.Map) {
  const features = [...fingers.value, ...placedSlips.value]
  if (!features.length) {
    m.flyTo({ center: props.fallbackCenter, zoom: props.fallbackZoom })
    return
  }
  let minLng = Infinity, minLat = Infinity, maxLng = -Infinity, maxLat = -Infinity
  for (const f of features) {
    const coords = f.geometry?.type === 'Point'
      ? [f.geometry.coordinates]
      : f.geometry?.type === 'LineString'
      ? f.geometry.coordinates
      : []
    for (const [lng, lat] of coords) {
      if (lng < minLng) minLng = lng
      if (lng > maxLng) maxLng = lng
      if (lat < minLat) minLat = lat
      if (lat > maxLat) maxLat = lat
    }
  }
  if (Number.isFinite(minLng)) {
    m.fitBounds(
      [
        [minLng, minLat],
        [maxLng, maxLat],
      ],
      { padding: 80, maxZoom: 18, duration: 0 },
    )
  }
}

onMounted(() => {
  if (!container.value) return
  const m = new maplibregl.Map({
    container: container.value,
    style: {
      version: 8,
      glyphs: 'https://demotiles.maplibre.org/font/{fontstack}/{range}.pbf',
      sources: {
        topo: {
          type: 'raster',
          tiles: [
            'https://cache.kartverket.no/v1/wmts/1.0.0/topo/default/webmercator/{z}/{y}/{x}.png',
          ],
          tileSize: 256,
          maxzoom: 18,
          attribution: '&copy; <a href="https://kartverket.no">Kartverket</a>',
        },
      },
      layers: [{ id: 'topo', type: 'raster', source: 'topo' }],
    },
    center: props.fallbackCenter,
    zoom: props.fallbackZoom,
  })

  m.addControl(new maplibregl.NavigationControl(), 'top-right')
  m.addControl(new maplibregl.FullscreenControl(), 'top-right')
  m.addControl(new maplibregl.ScaleControl({ maxWidth: 100, unit: 'metric' }), 'bottom-left')

  m.on('load', () => {
    m.addSource('harbor-fingers', {
      type: 'geojson',
      data: fingersCollection.value as never,
    })
    m.addSource('harbor-docks', {
      type: 'geojson',
      data: docksCollection.value as never,
    })

    m.addLayer({
      id: 'fingers-line',
      type: 'line',
      source: 'harbor-fingers',
      paint: {
        'line-color': '#0f172a',
        'line-width': 4,
      },
    })

    // Wide transparent hitbox so clicking *near* a finger line still selects it.
    m.addLayer({
      id: 'fingers-hitbox',
      type: 'line',
      source: 'harbor-fingers',
      paint: {
        'line-color': '#000',
        'line-opacity': 0,
        'line-width': 22,
      },
    })

    m.on('click', 'fingers-hitbox', (e) => {
      const raw = e.features?.[0]
      if (!raw) return
      const id = (raw.properties as { _id?: string })?._id ?? String(raw.id)
      emit('select-finger', { ...(raw as unknown as FingerFeature), id })
    })
    m.on('mouseenter', 'fingers-hitbox', () => {
      m.getCanvas().style.cursor = 'pointer'
    })
    m.on('mouseleave', 'fingers-hitbox', () => {
      m.getCanvas().style.cursor = ''
    })

    m.addLayer({
      id: 'docks-label',
      type: 'symbol',
      source: 'harbor-docks',
      layout: {
        'text-field': ['get', 'name'],
        'text-size': 14 * props.labelScale,
        'text-font': ['Open Sans Regular'],
        'text-allow-overlap': true,
      },
      paint: {
        'text-color': '#0f172a',
        'text-halo-color': '#ffffff',
        'text-halo-width': 2,
      },
    })

    syncSlipMarkers()
    fitToContent(m)
    emit('map-ready', m)
  })

  map.value = m
})

onUnmounted(() => {
  clearSlipMarkers()
  map.value?.remove()
  map.value = null
})

watch(
  [placedSlips, () => props.highlightSlipId, () => props.draggableSlips, () => props.labelScale],
  () => {
    if (map.value?.isStyleLoaded()) syncSlipMarkers()
  },
  { deep: true },
)

watch(
  () => props.labelScale,
  (s) => {
    const m = map.value
    if (!m || !m.getLayer('docks-label')) return
    m.setLayoutProperty('docks-label', 'text-size', 14 * s)
  },
)

watch(fingersCollection, (next) => {
  const src = map.value?.getSource('harbor-fingers') as
    | maplibregl.GeoJSONSource
    | undefined
  src?.setData(next as never)
})

watch(docksCollection, (next) => {
  const src = map.value?.getSource('harbor-docks') as
    | maplibregl.GeoJSONSource
    | undefined
  src?.setData(next as never)
})

defineExpose({
  getMap: () => map.value,
  fitToContent: () => map.value && fitToContent(map.value),
})
</script>

<template>
  <div ref="container" class="h-full w-full" />
</template>
