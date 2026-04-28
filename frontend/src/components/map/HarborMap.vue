<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, shallowRef } from 'vue'
import maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'

// In Vite dev mode, maplibre-gl's GeoJSON worker is bundled separately
// and the worker bundle is missing esbuild's __publicField helper, so
// every worker call (parsing, projecting) throws silently and GeoJSON
// sources never populate. Inject the helper into workers as a script
// before any Map is created.
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
  type HarborLayoutResponse,
  type SlipFeature,
  type FingerFeature,
} from '@/composables/useHarborLayout'

interface Props {
  layout: HarborLayoutResponse
  highlightSlipId?: string | null
  /** Default center if the layout has no placed features yet. */
  fallbackCenter?: [number, number]
  fallbackZoom?: number
}

const props = withDefaults(defineProps<Props>(), {
  highlightSlipId: null,
  fallbackCenter: () => [5.155736, 60.224303],
  fallbackZoom: 18,
})

const emit = defineEmits<{
  (e: 'select', slip: SlipFeature): void
  (e: 'select-finger', finger: FingerFeature): void
  (e: 'map-ready', map: maplibregl.Map): void
}>()

const container = ref<HTMLDivElement>()
const map = shallowRef<maplibregl.Map | null>(null)

const placedSlips = computed<SlipFeature[]>(() =>
  props.layout.features.filter(
    (f): f is SlipFeature => isSlip(f) && f.geometry != null,
  ),
)
const fingers = computed<FingerFeature[]>(() =>
  props.layout.features.filter(isFinger),
)

const slipsCollection = computed(() => ({
  type: 'FeatureCollection' as const,
  features: placedSlips.value.map((f) => ({
    ...f,
    properties: {
      ...f.properties,
      // maplibre re-keys feature.id to an internal integer in vector
      // tiles, so we duplicate the canonical UUID into properties.
      _id: f.id,
      _occupied: Boolean(
        f.properties.occupant_id || f.properties.occupant_last_name,
      ),
      _highlighted: f.id === props.highlightSlipId,
    },
  })),
}))

const fingersCollection = computed(() => ({
  type: 'FeatureCollection' as const,
  features: fingers.value.map((f) => ({
    ...f,
    properties: { ...f.properties, _id: f.id },
  })),
}))

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
    m.addSource('harbor-slips', {
      type: 'geojson',
      data: slipsCollection.value as never,
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

    m.addLayer({
      id: 'slips-circle',
      type: 'circle',
      source: 'harbor-slips',
      paint: {
        'circle-radius': [
          'interpolate', ['linear'], ['zoom'],
          15, 4,
          18, 9,
          20, 14,
        ],
        'circle-color': [
          'case',
          ['!', ['get', '_occupied']], 'transparent',
          ['==', ['get', 'assignment_type'], 'seasonal'], '#f59e0b',
          '#0ea5e9',
        ],
        'circle-stroke-color': [
          'case',
          ['get', '_highlighted'], '#dc2626',
          ['get', '_occupied'], '#0c4a6e',
          '#94a3b8',
        ],
        'circle-stroke-width': [
          'case',
          ['get', '_highlighted'], 3,
          1.5,
        ],
      },
    })

    m.addLayer({
      id: 'slips-label',
      type: 'symbol',
      source: 'harbor-slips',
      minzoom: 17,
      layout: {
        'text-field': [
          'case',
          ['has', 'occupant_last_name'], ['get', 'occupant_last_name'],
          ['get', 'number'],
        ],
        'text-size': 11,
        'text-offset': [0, 1.2],
        'text-anchor': 'top',
        'text-allow-overlap': false,
      },
      paint: {
        'text-color': '#0f172a',
        'text-halo-color': '#fff',
        'text-halo-width': 1.5,
      },
    })

    m.on('click', 'slips-circle', (e) => {
      const raw = e.features?.[0]
      if (!raw) return
      const id = (raw.properties as { _id?: string })?._id ?? String(raw.id)
      emit('select', { ...(raw as unknown as SlipFeature), id })
    })
    m.on('mouseenter', 'slips-circle', () => {
      m.getCanvas().style.cursor = 'pointer'
    })
    m.on('mouseleave', 'slips-circle', () => {
      m.getCanvas().style.cursor = ''
    })

    m.on('click', 'fingers-line', (e) => {
      const raw = e.features?.[0]
      if (!raw) return
      const id = (raw.properties as { _id?: string })?._id ?? String(raw.id)
      emit('select-finger', { ...(raw as unknown as FingerFeature), id })
    })
    m.on('mouseenter', 'fingers-line', () => {
      m.getCanvas().style.cursor = 'pointer'
    })
    m.on('mouseleave', 'fingers-line', () => {
      m.getCanvas().style.cursor = ''
    })

    fitToContent(m)
    emit('map-ready', m)
  })

  map.value = m
})

onUnmounted(() => {
  map.value?.remove()
  map.value = null
})

watch(slipsCollection, (next) => {
  const src = map.value?.getSource('harbor-slips') as
    | maplibregl.GeoJSONSource
    | undefined
  src?.setData(next as never)
})

watch(fingersCollection, (next) => {
  const src = map.value?.getSource('harbor-fingers') as
    | maplibregl.GeoJSONSource
    | undefined
  src?.setData(next as never)
})

defineExpose({ getMap: () => map.value, fitToContent: () => map.value && fitToContent(map.value) })
</script>

<template>
  <div ref="container" class="h-full w-full" />
</template>
