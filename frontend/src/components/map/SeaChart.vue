<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import type { MapMarker } from '@/composables/useMap'

const props = defineProps<{
  lat: number
  lng: number
  markers?: MapMarker[]
  clubName?: string
}>()

const container = ref<HTMLDivElement>()
let map: maplibregl.Map | null = null

const markerTypeColors: Record<string, string> = {
  waypoint: '#ef4444',
  buoy: '#f59e0b',
  hazard: '#dc2626',
  anchorage: '#3b82f6',
  harbor: '#059669',
}

function addMarkers() {
  if (!map) return

  document.querySelectorAll('.sea-chart-marker').forEach((el) => el.remove())

  if (props.markers) {
    for (const m of props.markers) {
      const color = markerTypeColors[m.marker_type] || '#6b7280'
      const el = document.createElement('div')
      el.className = 'sea-chart-marker'
      el.style.cssText = `width:14px;height:14px;border-radius:50%;background:${color};border:2px solid white;box-shadow:0 1px 3px rgba(0,0,0,.4);cursor:pointer;`

      new maplibregl.Marker({ element: el })
        .setLngLat([m.lng, m.lat])
        .setPopup(
          new maplibregl.Popup({ offset: 10 }).setHTML(
            `<strong>${m.label || m.marker_type}</strong>`
          )
        )
        .addTo(map)
    }
  }

  const el = document.createElement('div')
  el.className = 'sea-chart-marker'
  el.style.cssText = 'width:18px;height:18px;border-radius:50%;background:#059669;border:3px solid white;box-shadow:0 1px 4px rgba(0,0,0,.5);'
  new maplibregl.Marker({ element: el })
    .setLngLat([props.lng, props.lat])
    .setPopup(
      new maplibregl.Popup({ offset: 12 }).setHTML(
        `<strong>${props.clubName || 'Klubb'}</strong>`
      )
    )
    .addTo(map)
}

onMounted(() => {
  if (!container.value) return

  map = new maplibregl.Map({
    container: container.value,
    style: {
      version: 8,
      sources: {
        'kartverket-sjokart': {
          type: 'raster',
          tiles: [
            'https://cache.kartverket.no/v1/wmts/1.0.0/sjokartraster/default/webmercator/{z}/{y}/{x}.png',
          ],
          tileSize: 256,
          attribution: '&copy; <a href="https://kartverket.no">Kartverket</a>',
        },
      },
      layers: [
        {
          id: 'sjokart',
          type: 'raster',
          source: 'kartverket-sjokart',
        },
      ],
    },
    center: [props.lng, props.lat],
    zoom: 14,
  })

  map.addControl(new maplibregl.NavigationControl(), 'top-right')
  map.addControl(new maplibregl.FullscreenControl(), 'top-right')

  map.on('load', addMarkers)
})

watch(() => props.markers, addMarkers)

onUnmounted(() => {
  map?.remove()
  map = null
})
</script>

<template>
  <div class="relative h-full">
    <div ref="container" class="h-full w-full rounded-lg" />
    <slot name="overlay" />
  </div>
</template>
